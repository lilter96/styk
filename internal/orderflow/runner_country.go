package orderflow

import (
	"context"
	"slices"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) prepareCountryDispatch(ctx context.Context, work orders.Work, command orders.Command) error {
	if command.Action != orders.ActionCountrySet {
		return nil
	}
	if command.ActionInput == nil {
		return orders.ErrBlocked
	}
	decision, err := r.reviewedDecision(ctx, work)
	if err != nil {
		return err
	}
	if err = orders.ValidateCountryProposal(decision.Posting, *command.ActionInput); err != nil {
		return err
	}
	countries, err := readCountryDictionary(ctx, r.resolver, work.Scope, work.Credentials.Ozon, "")
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(countries, func(country orders.Country) bool { return country.ISOCode == command.ActionInput.CountryISOCode }) {
		return orders.ErrStale
	}
	return nil
}
func (r *Runner) readbackCountry(ctx context.Context, work orders.Work) error {
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return r.deferReadback(ctx, work, "COUNTRY_READBACK_CREDENTIALS_UNAVAILABLE")
	}
	posting, err := client.ReadOrderPosting(ctx, work.Scope, work.Command.PostingNumber)
	if err != nil {
		return r.deferReadback(ctx, work, "COUNTRY_REQUIREMENT_READBACK_PENDING")
	}
	hash, err := orders.PackingTargetFingerprint(posting)
	if err != nil || hash != work.Command.PostingAction.PackingTargetFingerprint {
		return r.deferReadback(ctx, work, "COUNTRY_TARGET_CHANGED")
	}
	receipt := work.Receipt
	evidence := orders.CountryEvidence{Scope: work.Scope, SKU: work.Command.PostingAction.Parameters.CountrySKU, Meaning: "country_requirement_resolution", TargetFingerprint: hash, RequirementResolved: orders.CountryRequirementCleared(posting, work.Command.PostingAction.Parameters.CountrySKU), CheckedAt: r.now()}
	acknowledged := receipt.HTTPStatus == 200 && receipt.CountryEvidence != nil && receipt.CountryEvidence.SKU == evidence.SKU && work.State != orders.StateUnknown
	if receipt.CountryEvidence != nil {
		evidence.GTDNeeded = receipt.CountryEvidence.GTDNeeded
	}
	receipt.CountryEvidence = &evidence
	if !acknowledged {
		return r.complete(ctx, work, orders.Completion{State: orders.StateUnknown, Code: "COUNTRY_WRITE_UNKNOWN_VALUE_NOT_OBSERVABLE", Receipt: receipt, ObservedPosting: &posting, NextRunAt: r.now().Add(r.options.PollInterval)})
	}
	if !evidence.RequirementResolved {
		return r.deferReadback(ctx, work, "COUNTRY_REQUIREMENT_NOT_YET_RESOLVED")
	}
	receipt.State = orders.StateReconciled
	return r.complete(ctx, work, orders.Completion{State: orders.StateReconciled, Code: "COUNTRY_REQUIREMENT_RESOLVED_ISO_VALUE_NOT_RETURNED", Receipt: receipt, ObservedPosting: &posting})
}
