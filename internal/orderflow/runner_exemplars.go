package orderflow

import (
	"context"
	"errors"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) prepareExemplarDispatch(ctx context.Context, work orders.Work, command orders.Command) (orders.Command, *orders.ExemplarSecret, error) {
	if !command.Action.IsExemplar() {
		if err := r.checkPackingExemplarProof(ctx, work, command); err != nil {
			return command, nil, err
		}
		return command, nil, nil
	}
	if r.options.ExemplarSealer == nil {
		return command, nil, orders.ErrBlocked
	}
	if command.Action == orders.ActionExemplarPrepare {
		return command, nil, nil
	}
	return r.prepareExemplarSetDispatch(ctx, work, command)
}

func (r *Runner) prepareExemplarSetDispatch(ctx context.Context, work orders.Work, command orders.Command) (orders.Command, *orders.ExemplarSecret, error) {
	if r.options.ExemplarReader == nil || command.ActionInput == nil || command.ActionInput.Exemplars == nil {
		return command, nil, orders.ErrBlocked
	}
	base, secret, err := r.options.ExemplarReader.ReadOrderExemplars(ctx, work.Scope, command.PostingAction.Parameters.ExemplarWorkID)
	if err != nil || !secret.Ready || secret.CredentialRevision != work.Credentials.Ozon || secret.TargetFingerprint != command.PostingAction.PackingTargetFingerprint {
		return command, nil, orders.ErrStale
	}
	decision, err := r.reviewedDecision(ctx, work)
	if err != nil {
		return command, nil, err
	}
	status, _, err := r.exemplarStatus(ctx, work)
	if err != nil || status.Status != "update_available" || !orders.ExemplarStatusMatches(base, status, false) {
		return command, nil, orders.ErrStale
	}
	complete, err := orders.ApplyExemplarInput(base, *command.ActionInput.Exemplars, decision.Posting)
	if err != nil {
		return command, nil, err
	}
	orders.CompleteExemplarRequirements(&complete, decision.Posting)
	if orders.ExemplarValuesEqual(base, complete) {
		return command, nil, orders.ErrConflict
	}
	if len(orders.ValidateExemplarSet(complete, decision.Posting)) != 0 {
		return command, nil, orders.ErrBlocked
	}
	input := *command.ActionInput
	input.Exemplars = exemplarSetInput(complete)
	command.ActionInput = &input
	protected, err := r.sealExemplars(ctx, work, complete)
	return command, protected, err
}

func exemplarSetInput(snapshot orders.ExemplarSnapshot) *orders.ExemplarSetInput {
	result := &orders.ExemplarSetInput{Products: make([]orders.ExemplarProductInput, 0, len(snapshot.Products))}
	for _, p := range snapshot.Products {
		result.Products = append(result.Products, orders.ExemplarProductInput{SKU: p.SKU, Exemplars: p.Exemplars})
	}
	return result
}

func (r *Runner) sealExemplars(ctx context.Context, work orders.Work, snapshot orders.ExemplarSnapshot) (*orders.ExemplarSecret, error) {
	if r.options.ExemplarSealer == nil {
		return nil, orders.ErrBlocked
	}
	secret, err := r.options.ExemplarSealer.SealOrderExemplars(work, snapshot, r.now())
	if err != nil {
		return nil, err
	}
	reader, ok := r.store.(interface {
		OrderConfig(context.Context, orders.Scope) (orders.Config, error)
	})
	if !ok {
		return nil, orders.ErrBlocked
	}
	config, err := reader.OrderConfig(ctx, work.Scope)
	if err != nil || config.Scope != work.Scope || config.Revision != work.ConfigRevision {
		return nil, orders.ErrStale
	}
	secret.ExpiresAt = secret.CreatedAt.Add(config.LabelRetention())
	secret.Public.ExpiresAt = secret.ExpiresAt
	return &secret, nil
}

func (r *Runner) recordPreparedExemplars(ctx context.Context, work orders.Work, receipt orders.Receipt, writeErr error) error {
	if writeErr != nil || receipt.PreparedExemplars == nil {
		return r.recordWrite(ctx, work, receipt, writeErr)
	}
	decision, err := r.reviewedDecision(ctx, work)
	if err != nil {
		return r.recordWrite(ctx, work, receipt, orders.ErrStale)
	}
	snapshot := *receipt.PreparedExemplars
	orders.CompleteExemplarRequirements(&snapshot, decision.Posting)
	if snapshot.ValidateFor(decision.Posting) != nil {
		return r.recordWrite(ctx, work, receipt, orders.ErrStale)
	}
	secret, err := r.sealExemplars(ctx, work, snapshot)
	if err != nil {
		return r.recordWrite(ctx, work, receipt, err)
	}
	receipt.PreparedExemplars = nil
	return r.complete(ctx, work, orders.Completion{State: orders.StateAcknowledged, Receipt: receipt, ExemplarSecret: secret, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func (r *Runner) exemplarStatus(ctx context.Context, work orders.Work) (orders.ExemplarStatus, orders.Provider, error) {
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return orders.ExemplarStatus{}, nil, err
	}
	reader, ok := client.(orders.ExemplarStatusReader)
	if !ok {
		return orders.ExemplarStatus{}, client, orders.ErrBlocked
	}
	status, err := reader.ReadOrderExemplarStatus(ctx, work.Scope, work.Command.PostingNumber)
	return status, client, err
}

func (r *Runner) pollExemplars(ctx context.Context, work orders.Work) error {
	if r.options.ExemplarReader == nil {
		return r.deferReadback(ctx, work, "EXEMPLAR_STORAGE_UNAVAILABLE")
	}
	expected, secret, err := r.options.ExemplarReader.ReadOrderExemplars(ctx, work.Scope, work.ID)
	if err != nil {
		if work.Command.Action == orders.ActionExemplarPrepare && work.State == orders.StateUnknown {
			return r.recoverUnknownExemplarIDs(ctx, work)
		}
		return r.deferReadback(ctx, work, "EXEMPLAR_INITIAL_OUTCOME_UNKNOWN")
	}
	return r.pollExemplarSnapshot(ctx, work, expected, secret)
}

func (r *Runner) pollExemplarSnapshot(ctx context.Context, work orders.Work, expected orders.ExemplarSnapshot, secret orders.ExemplarSecret) error {
	status, client, err := r.exemplarStatus(ctx, work)
	if errors.Is(err, orders.ErrSchemaDrift) {
		return r.deferReadback(ctx, work, "EXEMPLAR_SCHEMA_DRIFT")
	}
	if err != nil {
		return r.deferReadback(ctx, work, "EXEMPLAR_STATUS_PENDING")
	}
	posting, err := client.ReadOrderPosting(ctx, work.Scope, work.Command.PostingNumber)
	if err != nil {
		return r.deferReadback(ctx, work, "EXEMPLAR_TARGET_READ_PENDING")
	}
	hash, err := orders.PackingTargetFingerprint(posting)
	if err != nil || hash != secret.TargetFingerprint {
		return r.deferReadback(ctx, work, "EXEMPLAR_TARGET_CHANGED")
	}
	validation := work.Command.Action == orders.ActionExemplarSet
	if orders.ExemplarValidationFailed(status) {
		return r.complete(ctx, work, orders.Completion{State: orders.StateRejected, Code: "EXEMPLAR_PROVIDER_VALIDATION_FAILED"})
	}
	if !orders.ExemplarStatusMatches(expected, status, validation) {
		return r.deferReadback(ctx, work, "EXEMPLAR_VALUES_OR_VALIDATION_PENDING")
	}
	if !validation && status.Status != "update_available" && status.Status != "ship_available" {
		return r.deferReadback(ctx, work, "EXEMPLAR_IDS_PENDING")
	}
	receipt := work.Receipt
	receipt.State = orders.StateReconciled
	receipt.ExemplarEvidence = &orders.ExemplarEvidence{Scope: work.Scope, Verified: true, PackingReady: len(orders.ValidateExemplarSet(expected, posting)) == 0 && orders.ExemplarStatusMatches(expected, status, true), CredentialRevision: work.Credentials.Ozon, RequirementsHash: orders.ExemplarRequirementsHash(posting), TargetFingerprint: hash, Status: status.Status, SnapshotID: work.ID, IDs: orders.ExemplarIDs(expected), CheckedAt: r.now()}
	return r.complete(ctx, work, orders.Completion{State: orders.StateReconciled, Code: "EXEMPLAR_STATE_OBSERVED", Receipt: receipt, ObservedPosting: &posting})
}

func (r *Runner) checkPackingExemplarProof(ctx context.Context, work orders.Work, command orders.Command) error {
	if command.Action != orders.ActionPostingPack && command.Action != orders.ActionPostingSplit {
		return nil
	}
	id := command.PostingAction.Parameters.ExemplarWorkID
	if id == "" {
		return nil
	}
	if r.options.ExemplarReader == nil {
		return orders.ErrBlocked
	}
	expected, secret, err := r.options.ExemplarReader.ReadOrderExemplars(ctx, work.Scope, id)
	if err != nil || secret.CredentialRevision != work.Credentials.Ozon || secret.TargetFingerprint != command.PostingAction.PackingTargetFingerprint {
		return orders.ErrStale
	}
	status, _, err := r.exemplarStatus(ctx, work)
	if err != nil || !orders.ExemplarStatusMatches(expected, status, true) {
		return orders.ErrStale
	}
	return nil
}

// A lost prepare acknowledgement can only be resolved by independently observed
// provider-generated IDs. This proves current state, never causality or HTTP success.
func (r *Runner) recoverUnknownExemplarIDs(ctx context.Context, work orders.Work) error {
	status, client, err := r.exemplarStatus(ctx, work)
	if err != nil || (status.Status != "update_available" && status.Status != "ship_available") {
		return r.deferReadback(ctx, work, "EXEMPLAR_INITIAL_OUTCOME_UNKNOWN")
	}
	posting, err := client.ReadOrderPosting(ctx, work.Scope, work.Command.PostingNumber)
	if err != nil {
		return r.deferReadback(ctx, work, "EXEMPLAR_TARGET_READ_PENDING")
	}
	hash, err := orders.PackingTargetFingerprint(posting)
	if err != nil || hash != work.Command.PostingAction.PackingTargetFingerprint {
		return r.deferReadback(ctx, work, "EXEMPLAR_TARGET_CHANGED")
	}
	snapshot := orders.ExemplarSnapshot{PostingNumber: status.PostingNumber, Products: make([]orders.ExemplarProduct, 0, len(status.Products))}
	for _, p := range status.Products {
		snapshot.Products = append(snapshot.Products, orders.ExemplarProduct{UnsupportedFields: append(p.UnsupportedFields, "multi_box_qty_unknown"), SKU: p.SKU, Quantity: int64(len(p.Exemplars)), Exemplars: p.Exemplars})
	}
	orders.CompleteExemplarRequirements(&snapshot, posting)
	if snapshot.ValidateFor(posting) != nil {
		return r.deferReadback(ctx, work, "EXEMPLAR_IDS_INCOMPLETE_OR_CHANGED")
	}
	secret, err := r.sealExemplars(ctx, work, snapshot)
	if err != nil {
		return r.deferReadback(ctx, work, "EXEMPLAR_STORAGE_UNAVAILABLE")
	}
	receipt := work.Receipt
	receipt.State = orders.StateReconciled
	receipt.ExemplarEvidence = &orders.ExemplarEvidence{Scope: work.Scope, Verified: true, PackingReady: len(orders.ValidateExemplarSet(snapshot, posting)) == 0 && orders.ExemplarStatusMatches(snapshot, status, true), CredentialRevision: work.Credentials.Ozon, RequirementsHash: orders.ExemplarRequirementsHash(posting), TargetFingerprint: hash, Status: status.Status, SnapshotID: work.ID, IDs: orders.ExemplarIDs(snapshot), CheckedAt: r.now()}
	return r.complete(ctx, work, orders.Completion{State: orders.StateReconciled, Code: "EXEMPLAR_IDS_OBSERVED_AFTER_UNKNOWN_NOT_CAUSAL_PROOF", Receipt: receipt, ExemplarSecret: secret, ObservedPosting: &posting})
}
