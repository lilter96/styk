package orders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"
)

type ShipmentMoment string

const (
	OnHandoffConfirmed        ShipmentMoment = "ON_HANDOFF_CONFIRMED"
	OnOzonAcceptanceConfirmed ShipmentMoment = "ON_OZON_ACCEPTANCE_CONFIRMED"
)

// LifecyclePolicy is independent of the mirror grant. A configured native state
// is a seller declaration only after a newly observed, allowed transition.
type LifecyclePolicy struct {
	ID             string                   `json:"id"`
	Scope          Scope                    `json:"scope"`
	Revision       int64                    `json:"revision"`
	ConfigRevision int64                    `json:"config_revision"`
	OwnerID        string                   `json:"owner_id"`
	Enabled        bool                     `json:"enabled"`
	GrantedAt      time.Time                `json:"granted_at"`
	Mappings       []MerchantCommandMapping `json:"mappings"`
	DemandMoment   ShipmentMoment           `json:"demand_moment"`
	AutoDemand     bool                     `json:"auto_demand"`
	AutoReturn     bool                     `json:"auto_return"`
}

type MerchantCommandMapping struct {
	StateID      string   `json:"state_id"`
	FromStateIDs []string `json:"from_state_ids"`
	Action       Action   `json:"action"`
	Scheme       string   `json:"scheme"`
	EventKind    string   `json:"event_kind"`
	WholePosting bool     `json:"whole_posting"`
}

// MerchantIntent contains only exact document identities and provider revision
// facts. It is an observation, never a successful logistics/accounting result.
type MerchantIntent struct {
	ID                 string      `json:"id"`
	Digest             string      `json:"digest"`
	Scope              Scope       `json:"scope"`
	Correlation        Correlation `json:"correlation"`
	BeforeStateID      string      `json:"before_state_id"`
	AfterStateID       string      `json:"after_state_id"`
	BeforeRevision     string      `json:"before_revision"`
	AfterRevision      string      `json:"after_revision"`
	BeforeUpdatedAt    string      `json:"before_updated_at"`
	AfterUpdatedAt     string      `json:"after_updated_at"`
	ObservedAt         time.Time   `json:"observed_at"`
	ConfigRevision     int64       `json:"config_revision"`
	CredentialRevision int64       `json:"credential_revision"`
	Action             Action      `json:"action"`
	Scheme             string      `json:"scheme"`
	EventKind          string      `json:"event_kind"`
	WholePosting       bool        `json:"whole_posting"`
}

func (mapping MerchantCommandMapping) Validate() error {
	if !validUUID(mapping.StateID) || len(mapping.FromStateIDs) == 0 || len(mapping.FromStateIDs) > 20 || !slices.Contains([]string{"FBS", "rFBS"}, mapping.Scheme) {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(mapping.FromStateIDs))
	for _, state := range mapping.FromStateIDs {
		if !validUUID(state) || state == mapping.StateID || seen[state] {
			return ErrInvalid
		}
		seen[state] = true
	}
	return mapping.validateAction()
}

func (mapping MerchantCommandMapping) validateAction() error {
	wanted := map[Action]string{ActionPostingPack: "assembly_ready", ActionPostingShip: "handoff_confirmed", ActionPostingLastMile: "last_mile_confirmed", ActionPostingDeliver: "delivery_confirmed", ActionDemandCreate: "handoff_confirmed", ActionSalesReturnCreate: "return_received"}
	kind, found := wanted[mapping.Action]
	if !found || kind != mapping.EventKind || !mapping.WholePosting {
		return ErrInvalid
	}
	if mapping.Scheme == "FBS" && slices.Contains([]Action{ActionPostingShip, ActionPostingLastMile, ActionPostingDeliver}, mapping.Action) {
		return ErrInvalid
	}
	return nil
}

func (policy LifecyclePolicy) Validate() error {
	if !policy.Enabled {
		return nil
	}
	if !validUUID(policy.ID) || policy.Scope.Validate() != nil || policy.Revision < 1 || policy.ConfigRevision < 1 || policy.OwnerID == "" || policy.GrantedAt.IsZero() || len(policy.Mappings) == 0 || len(policy.Mappings) > 30 {
		return ErrInvalid
	}
	if policy.DemandMoment != OnHandoffConfirmed && policy.DemandMoment != OnOzonAcceptanceConfirmed {
		return ErrInvalid
	}
	// No verified Ozon acceptance event is currently exposed by the reader.
	if policy.AutoDemand && policy.DemandMoment == OnOzonAcceptanceConfirmed {
		return ErrBlocked
	}
	seen := make(map[string]bool, len(policy.Mappings))
	kinds := make(map[string]string, len(policy.Mappings))
	for _, mapping := range policy.Mappings {
		stateKey := mapping.Scheme + ":" + mapping.StateID
		key := stateKey + ":" + string(mapping.Action)
		if mapping.Validate() != nil || seen[key] || mapping.Action == ActionDemandCreate && !policy.AutoDemand || mapping.Action == ActionSalesReturnCreate && !policy.AutoReturn {
			return ErrInvalid
		}
		if kind, found := kinds[stateKey]; found && kind != mapping.EventKind {
			return ErrInvalid
		}
		kinds[stateKey] = mapping.EventKind
		seen[key] = true
	}
	return nil
}

func (policy LifecyclePolicy) Active(now time.Time, scope Scope, revision int64) bool {
	return policy.Enabled && policy.Validate() == nil && policy.Scope == scope && policy.ConfigRevision == revision && !now.Before(policy.GrantedAt)
}

func (policy LifecyclePolicy) Mapping(intent MerchantIntent) (MerchantCommandMapping, bool) {
	for _, mapping := range policy.Mappings {
		if mapping.Scheme == intent.Scheme && mapping.StateID == intent.AfterStateID && mapping.Action == intent.Action && mapping.EventKind == intent.EventKind && mapping.WholePosting == intent.WholePosting && slices.Contains(mapping.FromStateIDs, intent.BeforeStateID) {
			return mapping, true
		}
	}
	return MerchantCommandMapping{}, false
}

func (policy LifecyclePolicy) Allows(command Command, now time.Time) bool {
	intent := command.MerchantIntent
	if intent == nil || intent.Validate() != nil || !policy.Active(now, command.Scope, command.ConfigRevision) || command.LifecyclePolicyRevision != policy.Revision || command.AutomationGrantRevision != 0 || intent.Scope != command.Scope || intent.Correlation != command.Correlation || intent.Action != command.Action || intent.ConfigRevision != command.ConfigRevision || intent.CredentialRevision != command.Credentials.Moysklad || intent.AfterRevision != command.ExpectedTargetRevision {
		return false
	}
	if _, found := policy.Mapping(*intent); !found || now.Before(intent.ObservedAt) || now.Sub(intent.ObservedAt) > 15*time.Minute {
		return false
	}
	return command.PostingAction != nil && !command.Action.IsNativeMovement() && command.Action.Valid() && !command.Action.IsMirror()
}

func (intent MerchantIntent) Validate() error {
	if !validUUID(intent.ID) || intent.Scope.Validate() != nil || intent.Correlation.Validate() != nil || intent.Correlation.Scope != intent.Scope || intent.Correlation.CustomerOrderID == "" || !validUUID(intent.BeforeStateID) || !validUUID(intent.AfterStateID) || intent.BeforeStateID == intent.AfterStateID || intent.BeforeRevision == "" || intent.AfterRevision == "" || intent.BeforeRevision == intent.AfterRevision || intent.BeforeUpdatedAt == "" || intent.AfterUpdatedAt <= intent.BeforeUpdatedAt || intent.ObservedAt.IsZero() || intent.ConfigRevision < 1 || intent.CredentialRevision < 1 {
		return ErrInvalid
	}
	before, beforeErr := NativeParentVersionTime(intent.BeforeUpdatedAt)
	after, afterErr := NativeParentVersionTime(intent.AfterUpdatedAt)
	if beforeErr != nil || afterErr != nil || !after.After(before) {
		return ErrInvalid
	}
	if (MerchantCommandMapping{StateID: intent.AfterStateID, FromStateIDs: []string{intent.BeforeStateID}, Action: intent.Action, Scheme: intent.Scheme, EventKind: intent.EventKind, WholePosting: intent.WholePosting}).Validate() != nil {
		return ErrInvalid
	}
	expected, err := intent.Fingerprint()
	if err != nil || intent.Digest != expected {
		return ErrInvalid
	}
	return nil
}

func (intent MerchantIntent) Fingerprint() (string, error) {
	intent.ID, intent.Digest, intent.ObservedAt = "", "", time.Time{}
	raw, err := json.Marshal(intent)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

type MerchantIntentStatus struct {
	Intent MerchantIntent `json:"intent"`
	State  string         `json:"state"`
	Code   string         `json:"code,omitempty"`
	WorkID string         `json:"work_id,omitempty"`
}
