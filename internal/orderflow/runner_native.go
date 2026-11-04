package orderflow

import (
	"context"
	"errors"
	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) dispatchNative(ctx context.Context, work orders.Work) error {
	allowed, err := r.store.GuardOrderDispatch(ctx, orders.DispatchGuard{Work: work, Now: r.now()})
	if err != nil {
		return err
	}
	if !allowed {
		return r.stop(ctx, work, "NATIVE_AUTHORITY_CHANGED")
	}
	decision, err := r.reviewedDecision(ctx, work)
	if err == nil {
		_, err = r.freshSource(ctx, work, decision.Posting)
	}
	if err != nil {
		return r.stop(ctx, work, "FRESH_SOURCE_CHANGED_OR_UNAVAILABLE")
	}
	command, err := r.withNativeEvent(ctx, work.Command)
	if err != nil {
		return r.stop(ctx, work, "PHYSICAL_EVENT_PROOF_UNAVAILABLE")
	}
	work.Command = command
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "moysklad", work.Credentials.Moysklad)
	if err != nil {
		return r.stop(ctx, work, "SCOPED_TARGET_CREDENTIALS_UNAVAILABLE")
	}
	reader, ok := client.(orders.NativeDocumentReader)
	if !ok {
		return r.stop(ctx, work, "NATIVE_READBACK_UNSUPPORTED")
	}
	actual, err := reader.ReadNativeDocument(ctx, orders.NativeDocumentRequest{Scope: work.Scope, Correlation: command.NativeMovement.Correlation})
	if err == nil {
		return r.reconcileNative(ctx, work, client, actual)
	}
	if !errors.Is(err, orders.ErrNotFound) {
		return r.stop(ctx, work, "NATIVE_CORRELATION_UNAVAILABLE")
	}
	return r.bindAndWrite(ctx, work, client)
}

func (r *Runner) withNativeEvent(ctx context.Context, command orders.Command) (orders.Command, error) {
	verifier, ok := r.options.ActionReader.(orders.NativeEventReader)
	if !ok || command.NativeMovement == nil {
		return command, orders.ErrBlocked
	}
	if err := verifier.VerifyNativeMovementEvent(ctx, command); err != nil {
		return command, err
	}
	input, err := r.options.ActionReader.ReadOrderActionInput(ctx, command.Scope, command.PostingNumber, command.Action, command.NativeMovement.Event.ProtectedInput)
	if err != nil {
		return command, err
	}
	command.ActionInput = &input
	return command, nil
}

func (r *Runner) readbackNative(ctx context.Context, work orders.Work) error {
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "moysklad", work.Credentials.Moysklad)
	if err != nil {
		return r.deferReadback(ctx, work, "SCOPED_TARGET_CREDENTIALS_UNAVAILABLE")
	}
	reader, ok := client.(orders.NativeDocumentReader)
	if !ok || work.Command.NativeMovement == nil {
		return r.deferReadback(ctx, work, "NATIVE_READBACK_UNSUPPORTED")
	}
	correlation := work.Command.NativeMovement.Correlation
	if work.Receipt.ExternalID != "" {
		correlation.DocumentID = work.Receipt.ExternalID
	}
	actual, err := reader.ReadNativeDocument(ctx, orders.NativeDocumentRequest{Scope: work.Scope, Correlation: correlation})
	if err != nil {
		return r.deferReadback(ctx, work, "NATIVE_DOCUMENT_READBACK_PENDING")
	}
	return r.reconcileNative(ctx, work, client, actual)
}

func (r *Runner) reconcileNative(ctx context.Context, work orders.Work, client orders.Provider, actual orders.NativeDocumentObservation) error {
	if !orders.NativeDocumentMatches(work.Command, actual) || ordermoney.NativeDocumentTotal(work.Command, actual).Code != "" {
		return r.deferReadback(ctx, work, "NATIVE_DOCUMENT_FACTS_NOT_CONFIRMED")
	}
	reader, ok := client.(orders.TargetReader)
	if !ok {
		return r.deferReadback(ctx, work, "NATIVE_PARENT_READBACK_UNAVAILABLE")
	}
	target, err := reader.ReadCustomerOrder(ctx, work.Command.Correlation)
	if err != nil || !orders.NativeParentReadback(work.Command, target) || !ordermoney.NativeParentEffectObserved(work.Command, target) {
		return r.deferReadback(ctx, work, "NATIVE_PARENT_FACTS_NOT_CONFIRMED")
	}
	return r.complete(ctx, work, orders.Completion{State: orders.StateReconciled, Code: "NATIVE_DOCUMENT_FACTS_CONFIRMED", ObservedTarget: &target, ObservedNativeDocument: &actual})
}
