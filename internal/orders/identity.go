package orders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func validID(value string) bool {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func (scope Scope) Validate() error {
	if !validID(scope.Tenant) || !validID(scope.OzonAccount) || !validID(scope.MoyskladAccount) {
		return ErrInvalid
	}
	return nil
}

func (identity LineIdentity) Validate() error {
	if identity.SKU == "" && identity.OfferID == "" {
		return ErrInvalid
	}
	for _, value := range []string{identity.ProviderLineID, identity.SKU, identity.OfferID} {
		if value != "" && !validID(value) {
			return ErrInvalid
		}
	}
	return nil
}

func (identity LineIdentity) Key() string {
	return identityPart(identity.SKU) + identityPart(identity.OfferID) + identityPart(identity.ProviderLineID)
}

func identityPart(value string) string {
	return strconv.Itoa(len(value)) + ":" + value
}

// ValidateLineIdentities requires distinct provider line IDs to disambiguate
// repeated SKU+offer pairs. Neither quantity nor array order supplies identity.
func ValidateLineIdentities(identities []LineIdentity) error {
	seen := make(map[string]LineIdentity, len(identities))
	providerIDs := make(map[string]bool, len(identities))
	for _, identity := range identities {
		if err := identity.Validate(); err != nil {
			return fmt.Errorf("validate order line identity: %w", err)
		}
		pair := identityPart(identity.SKU) + identityPart(identity.OfferID)
		prior, exists := seen[pair]
		if exists && (prior.ProviderLineID == "" || identity.ProviderLineID == "") {
			return ErrAmbiguousLine
		}
		if identity.ProviderLineID != "" && providerIDs[identity.ProviderLineID] {
			return ErrAmbiguousLine
		}
		seen[pair] = identity
		if identity.ProviderLineID != "" {
			providerIDs[identity.ProviderLineID] = true
		}
	}
	return nil
}

// CorrelationFor deterministically reserves one MoySklad sync ID and safe marker
// for one scoped posting. A commercial order split has separate correlations.
func CorrelationFor(scope Scope, postingNumber string) (Correlation, error) {
	if scope.Validate() != nil || !validID(postingNumber) {
		return Correlation{}, ErrInvalid
	}
	identity := identityPart(scope.Tenant) + identityPart(scope.OzonAccount)
	identity += identityPart(scope.MoyskladAccount) + identityPart(postingNumber)
	sum := sha256.Sum256([]byte("styk-order-mirror-v1:" + identity))
	marker := "styk-order-v1-" + hex.EncodeToString(sum[:])
	sum[6] = (sum[6] & 0x0f) | 0x80
	sum[8] = (sum[8] & 0x3f) | 0x80
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	return Correlation{Scope: scope, PostingNumber: postingNumber, SyncID: uuid, Marker: marker}, nil
}

func (correlation Correlation) Validate() error {
	expected, err := CorrelationFor(correlation.Scope, correlation.PostingNumber)
	if err != nil {
		return err
	}
	if expected.SyncID != correlation.SyncID || expected.Marker != correlation.Marker {
		return ErrConflict
	}
	if correlation.CustomerOrderID != "" && !validID(correlation.CustomerOrderID) {
		return ErrInvalid
	}
	return nil
}

// CommandFingerprint includes only the frozen sanitized intent. Transient PII is
// excluded from JSON and is separately bound to the encrypted posting revision.
func CommandFingerprint(command Command) (string, error) {
	if err := command.Validate(); err != nil {
		return "", fmt.Errorf("validate order command fingerprint: %w", err)
	}
	data, err := json.Marshal(canonicalCommand(command))
	if err != nil {
		return "", fmt.Errorf("encode order command fingerprint: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalCommand(command Command) Command {
	if command.CustomerOrder != nil {
		write := *command.CustomerOrder
		write.Positions = slices.Clone(write.Positions)
		slices.SortFunc(write.Positions, func(a, b PositionWrite) int { return strings.Compare(a.Identity.Key(), b.Identity.Key()) })
		write.Attributes = slices.Clone(write.Attributes)
		slices.SortFunc(write.Attributes, func(a, b AttributeWrite) int { return strings.Compare(a.AttributeID, b.AttributeID) })
		command.CustomerOrder = &write
	}
	if command.PostingAction != nil {
		action := *command.PostingAction
		action.Lines = slices.Clone(action.Lines)
		slices.SortFunc(action.Lines, func(a, b LineIdentity) int { return strings.Compare(a.Key(), b.Key()) })
		command.PostingAction = &action
	}
	return command
}

// PostingFingerprint compares sanitized source facts as a multiset of stable
// line identities. Collection/observation times and opaque revision labels do
// not change the facts; provider timestamps and provider versions do.
func PostingFingerprint(posting Posting) (string, error) {
	if err := posting.Validate(); err != nil {
		return "", fmt.Errorf("validate posting fingerprint: %w", err)
	}
	posting.Evidence.Revision = ""
	posting.Evidence.ObservedAt = time.Time{}
	posting.Evidence.CheckedAt = time.Time{}
	posting.Evidence.CollectionID = ""
	posting.Evidence.LocalSequence = 0
	posting.Evidence.CollectionKind = ""
	posting.Lines = slices.Clone(posting.Lines)
	slices.SortFunc(posting.Lines, func(a, b PostingLine) int { return strings.Compare(a.Identity.Key(), b.Identity.Key()) })
	posting.ChildPostingNumbers = slices.Clone(posting.ChildPostingNumbers)
	slices.Sort(posting.ChildPostingNumbers)
	posting.AvailableActions = slices.Clone(posting.AvailableActions)
	slices.Sort(posting.AvailableActions)
	data, err := json.Marshal(posting)
	if err != nil {
		return "", fmt.Errorf("encode posting fingerprint: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
