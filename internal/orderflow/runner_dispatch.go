package orderflow

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) dispatch(ctx context.Context, work orders.Work) error {
	if !work.Command.Action.IsMirror() {
		return r.dispatchFulfillment(ctx, work)
	}
	return r.dispatchMirror(ctx, work)
}

func (r *Runner) dispatchMirror(ctx context.Context, work orders.Work) error {
	allowed, err := r.store.GuardOrderDispatch(ctx, orders.DispatchGuard{Work: work, Now: r.now()})
	if err != nil {
		return fmt.Errorf("check order dispatch authority: %w", err)
	}
	if !allowed {
		return r.stop(ctx, work, "CONFIG_MAPPING_OR_CREDENTIALS_CHANGED")
	}
	decision, err := r.reviewedDecision(ctx, work)
	if err != nil {
		return r.stop(ctx, work, "REVIEWED_FACTS_UNAVAILABLE")
	}
	if err = r.freshSource(ctx, work, decision.Posting); err != nil {
		return r.stop(ctx, work, "FRESH_SOURCE_CHANGED_OR_UNAVAILABLE")
	}
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "moysklad", work.Credentials.Moysklad)
	if err != nil {
		return r.stop(ctx, work, "SCOPED_TARGET_CREDENTIALS_UNAVAILABLE")
	}
	target, err := freshTarget(ctx, client, work.Command, decision.Target)
	if err != nil {
		return r.stop(ctx, work, "FRESH_TARGET_CHANGED_OR_UNAVAILABLE")
	}
	if target != nil && targetRepresents(work.Command, *target) {
		return r.reconcileTarget(ctx, work, *target)
	}
	return r.bindAndWrite(ctx, work, client)
}

func (r *Runner) dispatchFulfillment(ctx context.Context, work orders.Work) error {
	allowed, err := r.store.GuardOrderDispatch(ctx, orders.DispatchGuard{Work: work, Now: r.now()})
	if err != nil {
		return fmt.Errorf("check order dispatch authority: %w", err)
	}
	if !allowed {
		return r.stop(ctx, work, "CONFIG_MAPPING_OR_CREDENTIALS_CHANGED")
	}
	decision, sourceErr := r.reviewedDecision(ctx, work)
	if sourceErr == nil {
		sourceErr = r.freshSource(ctx, work, decision.Posting)
	}
	if sourceErr != nil {
		return r.stop(ctx, work, "FRESH_SOURCE_CHANGED_OR_UNAVAILABLE")
	}
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return r.stop(ctx, work, "SCOPED_SOURCE_CREDENTIALS_UNAVAILABLE")
	}
	var began atomic.Bool
	authorize := func(check context.Context) (bool, error) {
		allowed, err := r.store.GuardOrderDispatch(check, orders.DispatchGuard{Work: work, Now: r.now(), Begin: true})
		if allowed && err == nil {
			began.Store(true)
		}
		return allowed, err
	}
	if err := client.BindOrderIntent(work.Command, authorize); err != nil {
		return r.stop(ctx, work, "INTENT_BINDING_REJECTED")
	}
	send, cancel := context.WithDeadline(ctx, work.LeaseUntil)
	receipt, writeErr := client.WriteOrder(send, work.Command)
	cancel()
	if !began.Load() {
		return r.stop(ctx, work, "FINAL_DISPATCH_GUARD_NOT_ENTERED")
	}
	work.State, work.DispatchCount = orders.StateDispatching, 1
	return r.recordWrite(ctx, work, receipt, writeErr)
}

func (r *Runner) reviewedDecision(ctx context.Context, work orders.Work) (orders.Decision, error) {
	preview, err := r.previews.OrderPreview(ctx, work.Scope, work.PreviewID)
	if err != nil {
		return orders.Decision{}, err
	}
	if preview.Scope != work.Scope || preview.ConfigRevision != work.ConfigRevision || preview.Credentials != work.Credentials {
		return orders.Decision{}, orders.ErrStale
	}
	for _, decision := range preview.Decisions {
		if decision.Posting.PostingNumber != work.Command.PostingNumber || len(decision.Commands) != 1 {
			continue
		}
		fingerprint, fingerprintErr := orders.CommandFingerprint(decision.Commands[0])
		if fingerprintErr != nil || fingerprint != work.CommandFingerprint || decision.Posting.Evidence.Revision != work.Command.ExpectedSourceRevision {
			return orders.Decision{}, orders.ErrStale
		}
		return decision, nil
	}
	return orders.Decision{}, orders.ErrNotFound
}

func (r *Runner) freshSource(ctx context.Context, work orders.Work, reviewed orders.Posting) error {
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return err
	}
	fresh, err := client.ReadOrderPosting(ctx, work.Scope, work.Command.PostingNumber)
	if err != nil {
		return fmt.Errorf("read current source posting before order dispatch: %w", err)
	}
	if fresh.Scope != reviewed.Scope || fresh.PostingNumber != reviewed.PostingNumber || fresh.CommercialOrderID != reviewed.CommercialOrderID {
		return orders.ErrStale
	}
	before, err := orders.PostingFingerprint(withoutCustomerProjection(reviewed))
	if err != nil {
		return err
	}
	after, err := orders.PostingFingerprint(withoutCustomerProjection(fresh))
	if err != nil || before != after {
		return orders.ErrStale
	}
	return nil
}

func withoutCustomerProjection(posting orders.Posting) orders.Posting {
	posting.PII = nil
	posting.Customer = orders.ProtectedCustomer{}
	return posting
}

func freshTarget(ctx context.Context, client orders.Provider, command orders.Command, reviewed *orders.TargetObservation) (*orders.TargetObservation, error) {
	correlation, err := client.ReadOrderCorrelation(ctx, command.Correlation)
	if errors.Is(err, orders.ErrNotFound) && command.Action == orders.ActionMirrorCreate {
		return nil, nil
	}
	if err != nil || correlation.Validate() != nil || correlation.CustomerOrderID == "" {
		return nil, orders.ErrStale
	}
	reader, ok := client.(orders.TargetReader)
	if !ok {
		return nil, orders.ErrBlocked
	}
	target, err := reader.ReadCustomerOrder(ctx, correlation)
	if err != nil || !validTarget(command.Correlation, target) {
		return nil, orders.ErrStale
	}
	if targetRepresents(command, target) {
		return &target, nil
	}
	if command.Action == orders.ActionMirrorCreate || reviewed == nil || command.ExpectedTargetRevision != target.Revision || !sameTargetSnapshot(*reviewed, target) {
		return nil, orders.ErrStale
	}
	return &target, nil
}

func (r *Runner) bindAndWrite(ctx context.Context, work orders.Work, client orders.Provider) error {
	var began atomic.Bool
	authorize := func(check context.Context) (bool, error) {
		allowed, err := r.store.GuardOrderDispatch(check, orders.DispatchGuard{Work: work, Now: r.now(), Begin: true})
		if allowed && err == nil {
			began.Store(true)
		}
		return allowed, err
	}
	if err := client.BindOrderIntent(work.Command, authorize); err != nil {
		return r.stop(ctx, work, "INTENT_BINDING_REJECTED")
	}
	send, cancel := context.WithDeadline(ctx, work.LeaseUntil)
	receipt, err := client.WriteOrder(send, work.Command)
	cancel()
	if !began.Load() {
		return r.stop(ctx, work, "FINAL_DISPATCH_GUARD_NOT_ENTERED")
	}
	work.State, work.DispatchCount = orders.StateDispatching, 1
	return r.recordWrite(ctx, work, receipt, err)
}

func (r *Runner) recordWrite(ctx context.Context, work orders.Work, receipt orders.Receipt, writeErr error) error {
	if !validWriteReceipt(work.Command, receipt) {
		receipt = orders.Receipt{CommandID: work.Command.ID, State: orders.StateUnknown, Code: "WRITE_OUTCOME_UNKNOWN", ObservedAt: r.now()}
	}
	if writeErr != nil && receipt.State != orders.StateRejected {
		receipt.State, receipt.Code = orders.StateUnknown, "WRITE_OUTCOME_UNKNOWN"
	}
	if receipt.State == orders.StateReconciled {
		receipt.State = orders.StateAcknowledged
	}
	return r.complete(ctx, work, orders.Completion{State: receipt.State, Code: receipt.Code, Receipt: receipt, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func validWriteReceipt(command orders.Command, receipt orders.Receipt) bool {
	if receipt.CommandID != command.ID || receipt.ObservedAt.IsZero() || len(receipt.ExternalID) > 256 || len(receipt.TaskID) > 256 {
		return false
	}
	if receipt.HTTPStatus != 0 && (receipt.HTTPStatus < 100 || receipt.HTTPStatus > 599) {
		return false
	}
	return receipt.State == orders.StateAcknowledged || receipt.State == orders.StatePolling || receipt.State == orders.StateUnknown || receipt.State == orders.StateRejected || receipt.State == orders.StateReconciled
}
