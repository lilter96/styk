package orderflow

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) readback(ctx context.Context, work orders.Work) error {
	allowed, err := r.store.GuardOrderDispatch(ctx, orders.DispatchGuard{Work: work, Now: r.now()})
	if err != nil {
		return r.deferReadback(ctx, work, "READBACK_AUTHORITY_UNAVAILABLE")
	}
	if !allowed {
		return r.deferReadback(ctx, work, "READBACK_AUTHORITY_CHANGED")
	}
	if !work.Command.Action.IsMirror() {
		return r.readbackFulfillment(ctx, work)
	}
	decision, sourceErr := r.reviewedDecision(ctx, work)
	if sourceErr == nil {
		sourceErr = r.freshSource(ctx, work, decision.Posting)
	}
	target, err := r.readWorkTarget(ctx, work)
	if err != nil {
		return r.deferReadback(ctx, work, "TARGET_READBACK_PENDING")
	}
	if sourceErr != nil {
		return r.deferReadback(ctx, work, "SOURCE_CHANGED_REQUIRES_REVIEW")
	}
	if !targetRepresents(work.Command, target) {
		return r.deferReadback(ctx, work, "TARGET_FACTS_NOT_CONFIRMED")
	}
	return r.reconcileTarget(ctx, work, target)
}

func (r *Runner) readbackFulfillment(ctx context.Context, work orders.Work) error {
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return r.deferReadback(ctx, work, "SCOPED_SOURCE_CREDENTIALS_UNAVAILABLE")
	}
	posting, err := client.ReadOrderPosting(ctx, work.Scope, work.Command.PostingNumber)
	if err != nil {
		return r.deferReadback(ctx, work, "FULFILLMENT_READBACK_UNAVAILABLE")
	}
	return r.reconcileFulfillment(ctx, work, posting)
}

func (r *Runner) readWorkTarget(ctx context.Context, work orders.Work) (orders.TargetObservation, error) {
	if !work.Command.Action.IsMirror() {
		return r.readFulfillmentResult(ctx, work)
	}
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "moysklad", work.Credentials.Moysklad)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	correlation, err := client.ReadOrderCorrelation(ctx, work.Command.Correlation)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	reader, ok := client.(orders.TargetReader)
	if !ok {
		return orders.TargetObservation{}, orders.ErrBlocked
	}
	target, err := reader.ReadCustomerOrder(ctx, correlation)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	if !validTarget(work.Command.Correlation, target) {
		return orders.TargetObservation{}, orders.ErrStale
	}
	if work.Receipt.ExternalID != "" && target.Correlation.CustomerOrderID != work.Receipt.ExternalID {
		return orders.TargetObservation{}, orders.ErrConflict
	}
	return target, nil
}

func (r *Runner) reconcileTarget(ctx context.Context, work orders.Work, target orders.TargetObservation) error {
	correlation := target.Correlation
	return r.complete(ctx, work, orders.Completion{State: orders.StateReconciled, Code: "TARGET_FACTS_CONFIRMED", Correlation: &correlation, ObservedTarget: &target})
}

func (r *Runner) readFulfillmentResult(ctx context.Context, work orders.Work) (orders.TargetObservation, error) {
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	posting, err := client.ReadOrderPosting(ctx, work.Scope, work.Command.PostingNumber)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	return orders.TargetObservation{Scope: posting.Scope, ObservedAt: r.now(), Complete: true, Correlation: orders.Correlation{PostingNumber: posting.PostingNumber}}, nil
}

// reconcileFulfillment confirms a fulfillment work item only when the provider
// posting status matches the expected status for the dispatched action. When the
// status has not yet been applied, the work is deferred for the next polling
// cycle instead of being marked reconciled. After confirmation, the status is
// written back to MoySklad to keep both providers in sync.
func (r *Runner) reconcileFulfillment(ctx context.Context, work orders.Work, posting orders.Posting) error {
	expected := orders.ExpectedPostingStatus(work.Command.Action)

	// Label creation is verified through a separate provider task, not posting
	// status. Delegate to label task polling when the receipt is still polling.
	if work.Command.Action == orders.ActionPostingLabelCreate && work.Receipt.TaskID != "" && work.Receipt.State == orders.StatePolling {
		return r.pollLabelTask(ctx, work, work.Receipt.TaskID)
	}

	if expected != "" && posting.Status.Presence == orders.PresenceValue && posting.Status.Value != nil && *posting.Status.Value != expected {
		return r.deferReadback(ctx, work, "STATUS_NOT_APPLIED")
	}

	if expected != "" && posting.Status.Presence != orders.PresenceValue {
		return r.deferReadback(ctx, work, "STATUS_NOT_APPLIED")
	}

	// Write back confirmed status to MoySklad.
	if err := r.writeOrderStatusToMoySklad(ctx, work, &posting); err != nil {
		return r.deferReadback(ctx, work, "MOYSKLAD_WRITE_BACK_FAILED")
	}

	return r.complete(ctx, work, orders.Completion{State: orders.StateReconciled, Code: "FULFILLMENT_CONFIRMED", ObservedPosting: &posting, NextRunAt: r.now().Add(r.options.PollInterval)})
}

// writeOrderStatusToMoySklad updates the MoySklad customer order status to
// match the confirmed Ozon fulfillment status. It resolves the MoySklad order
// by correlation, finds the target state from config status mappings, and
// issues an idempotent PUT. Unknown outcomes are reconciled on the next cycle.
func (r *Runner) writeOrderStatusToMoySklad(ctx context.Context, work orders.Work, posting *orders.Posting) error {
	config, err := r.options.Service.store.OrderConfig(ctx, work.Scope)
	if err != nil {
		return fmt.Errorf("read MoySklad config: %w", err)
	}

	// Find the MoySklad state ID from status mappings, fall back to config.StateID.
	stateID := config.StateID
	if posting.Status.Presence == orders.PresenceValue && posting.Status.Value != nil {
		for _, mapping := range config.StatusMappings {
			if mapping.Status == *posting.Status.Value && mapping.StateID != "" {
				stateID = mapping.StateID
				break
			}
		}
	}

	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "moysklad", work.Credentials.Moysklad)
	if err != nil {
		return fmt.Errorf("resolve MoySklad client: %w", err)
	}

	writer, ok := client.(orders.TargetStatusWriter)
	if !ok {
		return orders.ErrBlocked
	}

	// Resolve the MoySklad order ID from correlation if not already known.
	orderID := work.Command.Correlation.CustomerOrderID
	if orderID == "" {
		correlation, err := client.ReadOrderCorrelation(ctx, work.Command.Correlation)
		if err != nil {
			return fmt.Errorf("resolve MoySklad order correlation: %w", err)
		}
		orderID = correlation.CustomerOrderID
	}
	if orderID == "" {
		return orders.ErrInvalid
	}

	if err := writer.WriteOrderStatus(ctx, work.Scope, orderID, stateID); err != nil {
		return fmt.Errorf("write order status to MoySklad: %w", err)
	}
	return nil
}

// pollLabelTask checks the Ozon label task until it completes or fails. It
// never retries the label creation itself; it only reads the task result.
func (r *Runner) pollLabelTask(ctx context.Context, work orders.Work, taskID string) error {
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return r.deferReadback(ctx, work, "SCOPED_SOURCE_CREDENTIALS_UNAVAILABLE")
	}
	reader, ok := client.(orders.LabelTaskReader)
	if !ok {
		return r.deferReadback(ctx, work, "LABEL_TASK_READER_UNAVAILABLE")
	}
	task, err := reader.ReadOrderLabelTask(ctx, work.Scope, taskID)
	if err != nil {
		return r.deferReadback(ctx, work, "LABEL_TASK_READBACK_UNAVAILABLE")
	}
	if task.Code == "completed" || task.ErrorCode != "" {
		if task.ErrorCode != "" {
			return r.complete(ctx, work, orders.Completion{State: orders.StateRejected, Code: "LABEL_TASK_FAILED:" + ordersSafeCode(task.ErrorCode), ObservedPosting: nil})
		}
		// Download the label PDF after task completion for durable storage.
		if task.FileURL != "" && r.options.LabelDownloader != nil {
			if _, err := r.options.LabelDownloader(ctx, task.FileURL); err != nil {
				return r.deferReadback(ctx, work, "LABEL_DOWNLOAD_FAILED")
			}
		}
		return r.complete(ctx, work, orders.Completion{State: orders.StateReconciled, Code: "LABEL_TASK_COMPLETED", ObservedPosting: nil})
	}
	return r.deferReadback(ctx, work, "LABEL_TASK_PENDING")
}

func ordersSafeCode(value string) string {
	if len(value) > 64 {
		return value[:64]
	}
	return value
}

func (r *Runner) deferReadback(ctx context.Context, work orders.Work, code string) error {
	state := orders.StatePolling
	if work.State == orders.StateUnknown || code == "SOURCE_CHANGED_REQUIRES_REVIEW" {
		state = orders.StateUnknown
	}
	return r.complete(ctx, work, orders.Completion{State: state, Code: code, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func sameTargetSnapshot(before, after orders.TargetObservation) bool {
	left, err := json.Marshal(canonicalTarget(before))
	if err != nil {
		return false
	}
	right, err := json.Marshal(canonicalTarget(after))
	return err == nil && string(left) == string(right)
}

func canonicalTarget(target orders.TargetObservation) orders.TargetObservation {
	target.ObservedAt = time.Time{}
	target.Fields.OwnedFields = nil
	target.Fields.Positions = nil
	target.Fields.Attributes = slices.Clone(target.Fields.Attributes)
	slices.SortFunc(target.Fields.Attributes, func(left, right orders.AttributeWrite) int {
		return strings.Compare(left.AttributeID, right.AttributeID)
	})
	target.Positions = slices.Clone(target.Positions)
	slices.SortFunc(target.Positions, func(left, right orders.TargetPosition) int { return strings.Compare(left.ID, right.ID) })
	return target
}
