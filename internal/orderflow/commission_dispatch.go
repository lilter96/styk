package orderflow

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

var errCommissionSourceChanged = errors.New("monthly realization source changed")
var errCommissionSourceComponents = errors.New("monthly realization components require accounting review")

func (r *CommissionRunner) dispatch(ctx context.Context, work orders.CommissionWork) error {
	allowed, err := r.store.GuardCommissionDispatch(ctx, work, false)
	if err != nil {
		return err
	}
	if !allowed {
		return r.stop(ctx, work, "FINANCE_AUTHORITY_CHANGED")
	}
	if err = r.preflight(ctx, work); err != nil {
		return r.stopPreflight(ctx, work, err)
	}
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "moysklad", work.Credentials.Moysklad)
	if err != nil {
		return r.stop(ctx, work, "FINANCE_TARGET_CREDENTIALS_UNAVAILABLE")
	}
	reader, ok := client.(orders.CommissionReader)
	if !ok {
		return r.stop(ctx, work, "FINANCE_TARGET_READ_UNSUPPORTED")
	}
	_, err = reader.ReadCommissionDraft(r.guardedReads(ctx, work), work.Plan, "")
	if err == nil {
		return r.stop(ctx, work, "FINANCE_EXISTING_DOCUMENT_REQUIRES_REVIEW")
	}
	if !errors.Is(err, orders.ErrNotFound) {
		return r.stop(ctx, work, "FINANCE_EXISTING_DOCUMENT_UNVERIFIED")
	}
	writer, ok := client.(orders.CommissionWriter)
	if !ok {
		return r.stop(ctx, work, "FINANCE_TARGET_WRITE_UNSUPPORTED")
	}
	return r.writeDraft(ctx, work, writer)
}

func (r *CommissionRunner) stopPreflight(ctx context.Context, work orders.CommissionWork, err error) error {
	if errors.Is(err, errCommissionSourceChanged) {
		return r.complete(ctx, work, orders.CommissionCompletion{State: orders.StateBlocked, Code: "FINANCE_SOURCE_CORRECTION_REQUIRES_REVIEW", SourceChanged: true})
	}
	if errors.Is(err, errCommissionSourceComponents) {
		return r.stop(ctx, work, "FINANCE_SOURCE_COMPONENTS_REQUIRE_REVIEW")
	}
	return r.stop(ctx, work, "FINANCE_SOURCE_OR_REFERENCES_CHANGED")
}

func (r *CommissionRunner) preflight(ctx context.Context, work orders.CommissionWork) error {
	review, err := r.options.Service.commissionReview(ctx, work.Scope, work.ConfigRevision)
	if err != nil {
		return err
	}
	references, err := r.options.Service.commissionReferences(ctx, review)
	if err != nil {
		return err
	}
	if !references.Matches(work.Plan.Policy) || references.Revision != work.Plan.References.Revision {
		return orders.ErrStale
	}
	return r.sourceCurrent(ctx, work)
}

func (r *CommissionRunner) sourceCurrent(ctx context.Context, work orders.CommissionWork) error {
	report, err := r.options.Service.RealizationReport(ctx, orders.RealizationRequest{Scope: work.Scope, Period: work.Plan.Source.Period})
	if err != nil {
		return err
	}
	if err = r.observer.ObserveCommissionReport(ctx, report); err != nil {
		return err
	}
	if report.Fingerprint != work.Plan.Source.Fingerprint {
		return errCommissionSourceChanged
	}
	if !ordermoney.CommissionPlanComponentsSupported(work.Plan) {
		return errCommissionSourceComponents
	}
	return nil
}

func (r *CommissionRunner) writeDraft(ctx context.Context, work orders.CommissionWork, writer orders.CommissionWriter) error {
	var began atomic.Bool
	authorize := func(check context.Context) (bool, error) {
		deadline := work.LeaseUntil
		if bounded, ok := check.Deadline(); ok && bounded.Before(deadline) {
			deadline = bounded
		}
		// The final guard performs database checks only. HTTP here could wait
		// for its own MS admission or invert Ozon/MS admission order against
		// an order worker. Provider facts were reread before write admission.
		fresh, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		if began.Load() {
			return false, orders.ErrConflict
		}
		allowed, err := r.store.GuardCommissionDispatch(fresh, work, true)
		if allowed && err == nil {
			began.Store(true)
		}
		return allowed, err
	}
	receipt, err := writer.WriteCommissionDraft(r.guardedReads(ctx, work), work.Plan, authorize)
	if !began.Load() {
		return r.stop(ctx, work, "FINANCE_FINAL_ADMISSION_BLOCKED")
	}
	work.State, work.DispatchCount = orders.StateDispatching, 1
	return r.finishWrite(ctx, work, receipt, err)
}

func (r *CommissionRunner) finishWrite(ctx context.Context, work orders.CommissionWork, receipt orders.Receipt, writeErr error) error {
	if receipt.CommandID != work.Plan.ID || writeErr != nil && receipt.State != orders.StateRejected || receipt.State != orders.StateAcknowledged && receipt.State != orders.StatePolling && receipt.State != orders.StateRejected {
		receipt.State, receipt.Code = orders.StateUnknown, "FINANCE_WRITE_OUTCOME_UNKNOWN"
	}
	receipt.CommandID = work.Plan.ID
	return r.complete(ctx, work, orders.CommissionCompletion{State: receipt.State, Code: receipt.Code, Receipt: receipt, NextRunAt: r.now().Add(r.options.PollInterval)})
}
