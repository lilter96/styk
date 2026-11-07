package orderflow

import (
	"context"
	"errors"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func (r *CommissionRunner) readback(ctx context.Context, work orders.CommissionWork) error {
	allowed, err := r.store.GuardCommissionDispatch(ctx, work, false)
	if err != nil {
		return err
	}
	if !allowed {
		return r.stop(ctx, work, "FINANCE_READBACK_AUTHORITY_CHANGED")
	}
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "moysklad", work.Credentials.Moysklad)
	if err != nil {
		return r.stop(ctx, work, "FINANCE_READBACK_CREDENTIALS_UNAVAILABLE")
	}
	reader, ok := client.(orders.CommissionReader)
	if !ok {
		return r.stop(ctx, work, "FINANCE_READBACK_UNSUPPORTED")
	}
	actual, err := reader.ReadCommissionDraft(r.guardedReads(ctx, work), work.Plan, work.Receipt.ExternalID)
	if err != nil {
		return r.deferReadback(ctx, work, err)
	}
	if ordermoney.CommissionPostedExternally(work.Plan, actual, work.Receipt.ExternalID) {
		return r.complete(ctx, work, orders.CommissionCompletion{State: orders.StateUnknown, Code: "FINANCE_DOCUMENT_POSTED_EXTERNALLY", Receipt: work.Receipt, Observation: &actual, SourceChanged: work.SourceChanged, NextRunAt: r.now().Add(r.options.PollInterval)})
	}
	changed, sourceErr := r.commissionSourceChanged(ctx, work)
	if sourceErr != nil {
		return r.complete(ctx, work, orders.CommissionCompletion{State: work.State, Code: "FINANCE_SOURCE_READBACK_UNAVAILABLE", Receipt: work.Receipt, Observation: &actual, NextRunAt: r.now().Add(r.options.PollInterval)})
	}
	if !ordermoney.CommissionDraftMatches(work.Plan, actual) {
		return r.complete(ctx, work, orders.CommissionCompletion{State: orders.StateUnknown, Code: "FINANCE_DOCUMENT_CONTENT_CONFLICT", Receipt: work.Receipt, Observation: &actual, SourceChanged: changed, NextRunAt: r.now().Add(r.options.PollInterval)})
	}
	code := "FINANCE_DRAFT_CONFIRMED"
	if work.Plan.Accounting != nil && !work.Plan.Accounting.Complete {
		code = "FINANCE_DRAFT_CONFIRMED_NEEDS_ACCOUNTING_REVIEW"
	}
	if changed {
		code = "FINANCE_SOURCE_CORRECTION_REQUIRES_REVIEW"
	}
	return r.complete(ctx, work, orders.CommissionCompletion{State: orders.StateReconciled, Code: code, Receipt: work.Receipt, Observation: &actual, SourceChanged: changed})
}

func (r *CommissionRunner) deferReadback(ctx context.Context, work orders.CommissionWork, readErr error) error {
	state, code := work.State, "FINANCE_READBACK_UNAVAILABLE"
	if errors.Is(readErr, orders.ErrNotFound) {
		code = "FINANCE_DOCUMENT_NOT_YET_OBSERVED"
	}
	if errors.Is(readErr, orders.ErrConflict) {
		state, code = orders.StateUnknown, "FINANCE_DOCUMENT_IDENTITY_AMBIGUOUS"
	}
	return r.complete(ctx, work, orders.CommissionCompletion{State: state, Code: code, Receipt: work.Receipt, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func (r *CommissionRunner) commissionSourceChanged(ctx context.Context, work orders.CommissionWork) (bool, error) {
	report, err := r.options.Service.RealizationReport(ctx, orders.RealizationRequest{Scope: work.Scope, Period: work.Plan.Source.Period})
	if err != nil {
		return work.SourceChanged, err
	}
	if err = r.observer.ObserveCommissionReport(ctx, report); err != nil {
		return work.SourceChanged, err
	}
	return work.SourceChanged || report.Fingerprint != work.Plan.Source.Fingerprint, nil
}
