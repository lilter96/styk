package orderflow

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func selectedPostings(postings []orders.Posting, requested []string) ([]orders.Posting, error) {
	selected := make(map[string]bool, len(requested))
	for _, number := range requested {
		if number == "" || selected[number] {
			return nil, orders.ErrInvalid
		}
		selected[number] = true
	}
	result := make([]orders.Posting, 0, min(len(postings), orders.MaxPagePostings))
	for _, posting := range postings {
		if len(requested) > 0 && !selected[posting.PostingNumber] {
			continue
		}
		delete(selected, posting.PostingNumber)
		if len(result) < orders.MaxPagePostings {
			result = append(result, posting)
		}
	}
	if len(selected) > 0 {
		return nil, orders.ErrNotFound
	}
	return result, nil
}

func (s *Service) planPreview(ctx context.Context, request orders.PreviewRequest, review orders.ReviewContext, postings []orders.Posting, automatic bool) (orders.Preview, error) {
	id, err := orderflowID()
	if err != nil {
		return orders.Preview{}, err
	}
	now := s.now()
	preview := orders.Preview{ID: id, Scope: request.Scope, ConfigRevision: review.Config.Revision,
		Credentials: review.Credentials, Actor: request.Actor, Automatic: automatic, CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(review.Config.PreviewTTLSeconds) * time.Second), Decisions: make([]orders.Decision, 0, len(postings))}
	for _, posting := range postings {
		decision, planErr := s.planPosting(ctx, review, posting, request.Actions, automatic, now)
		if planErr != nil {
			return orders.Preview{}, planErr
		}
		preview.Decisions = append(preview.Decisions, decision)
	}
	return preview, nil
}

func (s *Service) planPosting(ctx context.Context, review orders.ReviewContext, posting orders.Posting, actions []orders.Action, automatic bool, now time.Time) (orders.Decision, error) {
	posting.PII = nil
	decision := orders.Decision{Posting: posting, Commands: []orders.Command{}, Validation: sourceValidation(posting, review, now)}
	correlation, err := postingCorrelation(posting, review.Correlations)
	if err != nil {
		decision.Validation = append(decision.Validation, orders.Validation{Field: "correlation", Code: "correlation_conflict"})
		return decision, nil
	}
	decision.Correlation = correlation
	if len(decision.Validation) > 0 {
		return decision, nil
	}
	if containsPostingAction(actions) {
		decision.Validation = append(decision.Validation, orders.Validation{Field: "actions", Code: "outgoing_capability_unverified"})
		return decision, nil
	}
	return s.planMirror(ctx, review, decision, actions, automatic, now)
}

func sourceValidation(posting orders.Posting, review orders.ReviewContext, now time.Time) []orders.Validation {
	result := make([]orders.Validation, 0)
	config := review.Config
	if posting.Scope != config.Scope || posting.Validate() != nil || posting.Evidence.Origin != orders.OriginOzon {
		return append(result, orders.Validation{Field: "posting", Code: "invalid_source_facts"})
	}
	checked := posting.Evidence.CheckedAt
	if checked.IsZero() {
		checked = posting.Evidence.ObservedAt
	}
	if checked.After(now.Add(time.Minute)) || now.Sub(checked) > time.Duration(config.SourceFreshnessSeconds)*time.Second {
		result = append(result, orders.Validation{Field: "evidence.checked_at", Code: "source_stale"})
	}
	if !knownNonFBOScheme(posting, review.WarehouseMappings, now) {
		result = append(result, orders.Validation{Field: "scheme", Code: "scheme_unverified_or_out_of_scope"})
	}
	if _, active := review.ActivePostings[posting.PostingNumber]; active {
		result = append(result, orders.Validation{Field: "posting", Code: "target_fenced_unknown_or_pending"})
	}
	for _, drift := range posting.Drift {
		if strings.HasPrefix(drift.Code, "ORDERING_") {
			result = append(result, orders.Validation{Field: drift.Path, Code: "source_ordering_quarantined"})
		}
	}
	return result
}

func knownNonFBOScheme(posting orders.Posting, mappings []orders.WarehouseMappingRef, now time.Time) bool {
	if posting.Scheme.Validate() != nil {
		return false
	}
	if posting.Scheme.IsKnown() {
		return slices.Contains([]string{"FBS", "rFBS"}, *posting.Scheme.Value)
	}
	if !posting.WarehouseID.IsKnown() {
		return false
	}
	for _, mapping := range mappings {
		capability := mapping.Capability
		if mapping.Scope != posting.Scope || mapping.OzonWarehouseID != *posting.WarehouseID.Value || mapping.Validate() != nil || capability.Validate() != nil {
			continue
		}
		if now.Before(capability.ObservedAt) || !now.Before(capability.ValidUntil) {
			continue
		}
		if slices.Contains(capability.VerifiedDimensions, "scheme") && slices.Contains(capability.VerifiedDimensions, "warehouse") {
			return slices.Contains([]string{"FBS", "rFBS"}, capability.Scheme)
		}
	}
	return false
}

func postingCorrelation(posting orders.Posting, correlations []orders.Correlation) (orders.Correlation, error) {
	result, err := orders.CorrelationFor(posting.Scope, posting.PostingNumber)
	if err != nil {
		return orders.Correlation{}, err
	}
	count := 0
	for _, correlation := range correlations {
		if correlation.PostingNumber != posting.PostingNumber {
			continue
		}
		if correlation.Scope != posting.Scope || correlation.Validate() != nil {
			return orders.Correlation{}, orders.ErrConflict
		}
		result, count = correlation, count+1
	}
	if count > 1 {
		return orders.Correlation{}, orders.ErrConflict
	}
	return result, nil
}

func (s *Service) planMirror(ctx context.Context, review orders.ReviewContext, decision orders.Decision, actions []orders.Action, automatic bool, now time.Time) (orders.Decision, error) {
	warehouse, validation := postingWarehouse(decision.Posting, review)
	if validation.Code != "" {
		decision.Validation = append(decision.Validation, validation)
		return decision, nil
	}
	positions, validations := ordermoney.ProjectLines(decision.Posting, review.ProductMappings)
	decision.Validation = append(decision.Validation, validations...)
	write, validations := mirrorWrite(decision.Posting, review, positions)
	decision.Validation = append(decision.Validation, validations...)
	if len(decision.Validation) > 0 {
		return decision, nil
	}
	command, err := mirrorCommand(review, decision, warehouse, write, automatic, now)
	if err != nil {
		return orders.Decision{}, err
	}
	if len(actions) > 0 && !slices.Contains(actions, command.Action) {
		decision.Validation = append(decision.Validation, orders.Validation{Field: "actions", Code: "mirror_action_not_selected"})
		return decision, nil
	}
	return s.finishMirrorPlan(ctx, review, decision, command, automatic, now)
}

func postingWarehouse(posting orders.Posting, review orders.ReviewContext) (orders.WarehouseMappingRef, orders.Validation) {
	if !posting.WarehouseID.IsKnown() {
		return orders.WarehouseMappingRef{}, orders.Validation{Field: "warehouse_id", Code: "warehouse_unknown"}
	}
	var result orders.WarehouseMappingRef
	count := 0
	for _, mapping := range review.WarehouseMappings {
		if mapping.Scope == posting.Scope && mapping.OzonWarehouseID == *posting.WarehouseID.Value && mapping.Validate() == nil {
			result, count = mapping, count+1
		}
	}
	if count != 1 || result.MoyskladStoreID != review.Config.StoreID {
		return result, orders.Validation{Field: "warehouse_id", Code: "confirmed_warehouse_mapping_required"}
	}
	return result, orders.Validation{}
}

func mirrorCommand(review orders.ReviewContext, decision orders.Decision, warehouse orders.WarehouseMappingRef, write orders.CustomerOrderWrite, automatic bool, now time.Time) (orders.Command, error) {
	id, err := orderflowID()
	if err != nil {
		return orders.Command{}, err
	}
	action := orders.ActionMirrorCreate
	if decision.Correlation.CustomerOrderID != "" {
		action = orders.ActionMirrorUpdate
	}
	command := orders.Command{ID: id, Scope: decision.Posting.Scope, Action: action, CommercialOrderID: decision.Posting.CommercialOrderID,
		PostingNumber: decision.Posting.PostingNumber, Correlation: decision.Correlation, ConfigRevision: review.Config.Revision,
		Credentials: review.Credentials, ExpectedSourceRevision: decision.Posting.Evidence.Revision, SourceOrigin: decision.Posting.Evidence.Origin,
		SourceCommandID: decision.Posting.Evidence.SourceCommandID, Warehouse: warehouse, CustomerOrder: &write, IdempotencyKey: id, CreatedAt: now}
	if automatic && review.Config.Automation != nil {
		command.AutomationGrantRevision = review.Config.Automation.Revision
	}
	return command, nil
}

func containsPostingAction(actions []orders.Action) bool {
	for _, action := range actions {
		if !action.IsMirror() {
			return true
		}
	}
	return false
}

func orderflowID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate order synchronization identity: %w", err)
	}
	value[6], value[8] = (value[6]&0x0f)|0x40, (value[8]&0x3f)|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:]), nil
}
