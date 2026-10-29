package orders

import (
	"fmt"
	"strings"
	"time"
)

const (
	MaxAutomationGrantTTL = 24 * time.Hour
	MaxPIIRetention       = 30 * 24 * time.Hour
	MaxPreviewTTL         = 15 * time.Minute
	MaxPagePostings       = 100
)

func (config Config) Validate() error {
	if config.CurrencyID != "" && !validUUID(config.CurrencyID) {
		return ErrInvalid
	}
	if err := config.Scope.Validate(); err != nil {
		return fmt.Errorf("validate order configuration scope: %w", err)
	}
	if config.Revision < 0 || !config.validBounds() {
		return ErrInvalid
	}
	if err := config.validateReferences(); err != nil {
		return err
	}
	if err := config.validateFieldMappings(); err != nil {
		return err
	}
	if err := config.validateStatusMappings(); err != nil {
		return err
	}
	if config.Automation != nil {
		return config.validateAutomation()
	}
	return nil
}

func (config Config) validBounds() bool {
	if config.PIIEnabled && (config.PIIRetentionSeconds < 1 || config.PIIRetentionSeconds > int64(MaxPIIRetention/time.Second)) {
		return false
	}
	if !config.PIIEnabled && config.PIIRetentionSeconds != 0 {
		return false
	}
	if !config.Enabled && !config.TestEnabled && config.IntervalSeconds == 0 && config.SourceFreshnessSeconds == 0 && config.PreviewTTLSeconds == 0 {
		return true
	}
	return config.IntervalSeconds >= 30 && config.IntervalSeconds <= 86400 &&
		config.SourceFreshnessSeconds > 0 && config.SourceFreshnessSeconds <= 86400 &&
		config.PreviewTTLSeconds > 0 && config.PreviewTTLSeconds <= int64(MaxPreviewTTL/time.Second)
}

func (config Config) validateReferences() error {
	for _, id := range []string{config.OrganizationID, config.AgentID, config.StoreID, config.StateID, config.SalesChannelID} {
		if id == "" && !config.Enabled && !config.TestEnabled {
			continue
		}
		if !validID(id) {
			return fmt.Errorf("explicit organization, agent, store, state and sales channel are required: %w", ErrInvalid)
		}
	}
	return nil
}

func (config Config) validateFieldMappings() error {
	seen := make(map[string]bool, len(config.FieldMappings))
	for _, mapping := range config.FieldMappings {
		if !safePath(mapping.Source) || !writableTarget(mapping.Target) || seen[mapping.Target+":"+mapping.AttributeID] {
			return ErrInvalid
		}
		if (mapping.Target == "attribute") != (mapping.AttributeID != "") {
			return ErrInvalid
		}
		if mapping.AttributeID != "" && !validUUID(mapping.AttributeID) {
			return ErrInvalid
		}
		if strings.HasPrefix(mapping.Source, "customer.") && !config.PIIEnabled {
			return ErrBlocked
		}
		seen[mapping.Target+":"+mapping.AttributeID] = true
	}
	return nil
}

func writableTarget(target string) bool {
	switch target {
	case "name", "description", "moment", "delivery_at", "applicable", "state", "attribute":
		return true
	case "positions.quantity", "positions.reserve", "positions.price", "positions.vat", "vat_enabled", "vat_included":
		return true
	default:
		return false
	}
}

func (config Config) validateStatusMappings() error {
	seen := make(map[string]bool, len(config.StatusMappings))
	for _, mapping := range config.StatusMappings {
		if !validID(mapping.Status) || !validID(mapping.StateID) || mapping.Substatus.Validate() != nil {
			return ErrInvalid
		}
		key := identityPart(mapping.Status) + string(mapping.Substatus.Presence)
		if mapping.Substatus.Value != nil {
			key += identityPart(*mapping.Substatus.Value)
		}
		if seen[key] {
			return ErrConflict
		}
		seen[key] = true
	}
	return nil
}

func (config Config) validateAutomation() error {
	grant := config.Automation
	if grant.Scope != config.Scope || grant.ConfigRevision != config.Revision {
		return ErrStale
	}
	return grant.Validate()
}

func (grant AutomationGrant) Validate() error {
	if grant.Scope.Validate() != nil || !validID(grant.ID) || !validID(grant.OwnerID) || grant.Revision < 1 || grant.ConfigRevision < 1 {
		return ErrInvalid
	}
	if grant.SourceAccount != grant.Scope.OzonAccount || grant.GrantedAt.IsZero() || !grant.ExpiresAt.After(grant.GrantedAt) {
		return ErrInvalid
	}
	if grant.ExpiresAt.Sub(grant.GrantedAt) > MaxAutomationGrantTTL || len(grant.Actions) == 0 {
		return ErrInvalid
	}
	seen := make(map[Action]bool, len(grant.Actions))
	for _, action := range grant.Actions {
		if !action.Valid() || seen[action] {
			return ErrInvalid
		}
		seen[action] = true
	}
	for _, field := range grant.Fields {
		if !validGrantField(field) {
			return ErrInvalid
		}
	}
	return nil
}

func (grant AutomationGrant) Active(now time.Time, scope Scope, revision int64) bool {
	return grant.Validate() == nil && grant.Scope == scope && grant.ConfigRevision == revision &&
		grant.RevokedAt == nil && !now.Before(grant.GrantedAt) && now.Before(grant.ExpiresAt)
}

func (secret PostingSecret) Validate() error {
	if secret.Scope.Validate() != nil || !validID(secret.PostingNumber) || !validID(secret.Purpose) || secret.Revision < 1 {
		return ErrInvalid
	}
	if len(secret.Ciphertext) == 0 || len(secret.Ciphertext) > 65536 || secret.CreatedAt.IsZero() || !secret.ExpiresAt.After(secret.CreatedAt) {
		return ErrInvalid
	}
	if secret.ExpiresAt.Sub(secret.CreatedAt) > MaxPIIRetention {
		return ErrInvalid
	}
	return nil
}

func (page SourcePage) Validate() error {
	if page.Scope.Validate() != nil || !validID(page.Stream) || !validID(page.RefreshKey) || !validID(page.IdempotencyKey) || !validID(page.FenceToken) {
		return ErrInvalid
	}
	if page.ConfigRevision < 1 || page.Credentials.Ozon < 1 || page.Credentials.Moysklad < 1 {
		return ErrInvalid
	}
	if page.ExpectedCheckpointRevision < 0 || len(page.Postings) > MaxPagePostings || page.WindowFrom.IsZero() || !page.WindowTo.After(page.WindowFrom) {
		return ErrInvalid
	}
	if page.StartedAt.IsZero() || page.FinishedAt.Before(page.StartedAt) || (!page.Complete && (page.NextCursor == "" || page.NextCursor == page.ExpectedCursor)) {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(page.Postings))
	for _, posting := range page.Postings {
		if posting.Scope != page.Scope || seen[posting.PostingNumber] || posting.Validate() != nil {
			return ErrInvalid
		}
		if page.FreshDetail && posting.Evidence.CollectionKind != CollectionSerialDetail {
			return ErrInvalid
		}
		seen[posting.PostingNumber] = true
	}
	for _, secret := range page.Secrets {
		if secret.Scope != page.Scope || !seen[secret.PostingNumber] || secret.Validate() != nil {
			return ErrInvalid
		}
	}
	return nil
}

func safePath(path string) bool {
	if path == "" || len(path) > 256 {
		return false
	}
	for _, char := range path {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("$._[]*:-", char) {
			continue
		}
		return false
	}
	return true
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return false
		}
	}
	return true
}
