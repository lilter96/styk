package orderactions

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/lilter96/styk/internal/orders"
)

const maxFBSLines = 1000

// These inputs are STYK validation types, not Ozon request DTOs. Provider IDs
// remain strings, including identifiers larger than JavaScript's integer range.
type FBSQuantity struct {
	Identity orders.LineIdentity
	Quantity orders.Decimal
}

type FBSPackage struct {
	Lines []FBSQuantity
}

type FBSReasonCatalogue struct {
	Scope          orders.Scope
	PostingNumber  string
	SourceRevision string
	ReasonIDs      []string
	ObservedAt     time.Time
	ValidUntil     time.Time
}

type FBSPartialCancellation struct {
	ReasonID  string
	Catalogue FBSReasonCatalogue
	Lines     []FBSQuantity
}

// ValidateFBSPackages requires exact conservation of remaining source units.
// A repeated line may be distributed across packages but not repeated within
// one package. Bounds are local validation limits, not claimed provider limits.
func ValidateFBSPackages(posting orders.Posting, packages []FBSPackage) error {
	remaining, err := fbsRemainingUnits(posting)
	if err != nil {
		return err
	}
	if len(packages) == 0 || len(packages) > maxFBSLines {
		return orders.ErrInvalid
	}
	for _, box := range packages {
		if err := fbsAllocateUnits(remaining, box.Lines); err != nil {
			return err
		}
	}
	if !fbsFullyAllocated(remaining) {
		return orders.ErrBlocked
	}
	return nil
}

// ValidateFBSPartialCancellation requires actual positive quantities and an
// exact reason ID from a fresh catalogue for this posting and source revision.
// A complete cancellation cannot masquerade as a partial line cancellation.
func ValidateFBSPartialCancellation(posting orders.Posting, input FBSPartialCancellation, now time.Time) error {
	if err := input.Catalogue.validate(posting, now); err != nil {
		return err
	}
	if !fbsNumericID(input.ReasonID) || !slices.Contains(input.Catalogue.ReasonIDs, input.ReasonID) {
		return orders.ErrInvalid
	}
	remaining, err := fbsRemainingUnits(posting)
	if err != nil {
		return err
	}
	if err := fbsAllocateUnits(remaining, input.Lines); err != nil {
		return err
	}
	if fbsFullyAllocated(remaining) {
		return orders.ErrConflict
	}
	return nil
}

func (catalogue FBSReasonCatalogue) validate(posting orders.Posting, now time.Time) error {
	if catalogue.Scope != posting.Scope || catalogue.PostingNumber != posting.PostingNumber || catalogue.SourceRevision != posting.Evidence.Revision {
		return orders.ErrStale
	}
	if now.IsZero() || catalogue.ObservedAt.IsZero() || catalogue.ObservedAt.After(now) || !catalogue.ValidUntil.After(now) {
		return orders.ErrStale
	}
	if len(catalogue.ReasonIDs) == 0 || len(catalogue.ReasonIDs) > maxFBSLines {
		return orders.ErrBlocked
	}
	seen := make(map[string]bool, len(catalogue.ReasonIDs))
	for _, reason := range catalogue.ReasonIDs {
		if !fbsNumericID(reason) || seen[reason] {
			return orders.ErrInvalid
		}
		seen[reason] = true
	}
	return nil
}

// FBSReservationFrontier returns children only after complete lineage and unit
// conservation checks. It can never reserve the full parent plus its children.
// Further nested splits require a separately reconciled frontier.
func FBSReservationFrontier(parent orders.Posting, children []orders.Posting) ([]string, error) {
	remaining, err := fbsRemainingUnits(parent)
	if err != nil {
		return nil, err
	}
	if len(children) < 2 || len(children) != len(parent.ChildPostingNumbers) || len(children) > maxFBSLines {
		return nil, orders.ErrBlocked
	}
	numbers := make([]string, 0, len(children))
	for _, child := range children {
		if err := fbsSplitChild(parent, child, numbers); err != nil {
			return nil, err
		}
		lines, err := fbsChildQuantities(child)
		if err != nil {
			return nil, err
		}
		if err := fbsAllocateUnits(remaining, lines); err != nil {
			return nil, err
		}
		numbers = append(numbers, child.PostingNumber)
	}
	if !fbsFullyAllocated(remaining) {
		return nil, orders.ErrBlocked
	}
	slices.Sort(numbers)
	return numbers, nil
}

func fbsSplitChild(parent, child orders.Posting, seen []string) error {
	if child.Validate() != nil || child.Scope != parent.Scope || child.CommercialOrderID != parent.CommercialOrderID || child.Evidence.Origin != orders.OriginOzon {
		return orders.ErrStale
	}
	if !fbsKnownString(child.Scheme, "FBS") || !fbsKnownString(child.ParentPostingNumber, parent.PostingNumber) {
		return orders.ErrBlocked
	}
	if child.PostingNumber == parent.PostingNumber || slices.Contains(seen, child.PostingNumber) || !slices.Contains(parent.ChildPostingNumbers, child.PostingNumber) {
		return orders.ErrConflict
	}
	if len(child.ChildPostingNumbers) != 0 {
		return orders.ErrBlocked
	}
	return nil
}

func fbsChildQuantities(child orders.Posting) ([]FBSQuantity, error) {
	units, err := fbsRemainingUnits(child)
	if err != nil {
		return nil, err
	}
	lines := make([]FBSQuantity, 0, len(child.Lines))
	for _, line := range child.Lines {
		quantity := units[line.Identity.Key()]
		if quantity > 0 {
			lines = append(lines, FBSQuantity{Identity: line.Identity, Quantity: orders.Decimal(strconv.FormatUint(quantity, 10))})
		}
	}
	return lines, nil
}

func fbsRemainingUnits(posting orders.Posting) (map[string]uint64, error) {
	if posting.Validate() != nil || !fbsKnownString(posting.Scheme, "FBS") || posting.Evidence.Origin != orders.OriginOzon {
		return nil, orders.ErrBlocked
	}
	if len(posting.Lines) == 0 || len(posting.Lines) > maxFBSLines {
		return nil, orders.ErrInvalid
	}
	units := make(map[string]uint64, len(posting.Lines))
	for _, line := range posting.Lines {
		quantity, err := fbsLineUnits(line)
		if err != nil {
			return nil, err
		}
		units[line.Identity.Key()] = quantity
	}
	return units, nil
}

func fbsLineUnits(line orders.PostingLine) (uint64, error) {
	quantity, err := fbsUnits(line.Quantity)
	if err != nil {
		return 0, err
	}
	if !line.CancelledQuantity.IsKnown() || line.CancelledQuantity.Validate() != nil {
		return 0, orders.ErrBlocked
	}
	cancelled, err := fbsUnits(*line.CancelledQuantity.Value)
	if err != nil {
		return 0, err
	}
	if cancelled > quantity {
		return 0, orders.ErrConflict
	}
	return quantity - cancelled, nil
}

func fbsAllocateUnits(remaining map[string]uint64, lines []FBSQuantity) error {
	if len(lines) == 0 || len(lines) > maxFBSLines {
		return orders.ErrInvalid
	}
	identities := make([]orders.LineIdentity, 0, len(lines))
	for _, line := range lines {
		identities = append(identities, line.Identity)
	}
	if err := orders.ValidateLineIdentities(identities); err != nil {
		return err
	}
	for _, line := range lines {
		quantity, err := fbsUnits(line.Quantity)
		if err != nil || quantity == 0 {
			return orders.ErrInvalid
		}
		key := line.Identity.Key()
		available, exists := remaining[key]
		if !exists {
			return orders.ErrStale
		}
		if quantity > available {
			return orders.ErrConflict
		}
		remaining[key] = available - quantity
	}
	return nil
}

func fbsFullyAllocated(remaining map[string]uint64) bool {
	for _, quantity := range remaining {
		if quantity != 0 {
			return false
		}
	}
	return true
}

func fbsUnits(quantity orders.Decimal) (uint64, error) {
	if quantity.Validate() != nil || strings.ContainsAny(string(quantity), ".-") {
		return 0, orders.ErrInvalid
	}
	value, err := strconv.ParseUint(string(quantity), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("validate bounded FBS units: %w", orders.ErrInvalid)
	}
	return value, nil
}

func fbsNumericID(value string) bool {
	if !fbsText(value) || len(value) > 128 || value[0] == '0' {
		return false
	}
	return strings.Trim(value, "0123456789") == ""
}

func fbsText(value string) bool {
	if len(value) == 0 || len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}
