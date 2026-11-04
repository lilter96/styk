package postgres

import (
	"context"
	"reflect"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func validateOrderCountryCommand(ctx context.Context, q ordersQuerier, posting orders.Posting, command orders.Command) error {
	if command.Action != orders.ActionCountrySet {
		return nil
	}
	input := command.PostingAction.Parameters
	if !input.CountryProvided || !input.CountryConfirmed || command.PostingAction.ProtectedInput == nil {
		return orders.ErrBlocked
	}
	if !posting.Status.IsKnown() || *posting.Status.Value != "awaiting_packaging" {
		return orders.ErrStale
	}
	required, err := orders.CountryRequiredSKUs(posting)
	if err != nil {
		return err
	}
	found := false
	for _, sku := range required {
		found = found || sku == input.CountrySKU
	}
	hash, err := orders.PackingTargetFingerprint(posting)
	if err != nil || !found || hash != command.PostingAction.PackingTargetFingerprint {
		return orders.ErrStale
	}
	_, err = readOrderActionSecret(ctx, q, command.Scope, command.PostingNumber, command.Action, *command.PostingAction.ProtectedInput)
	return err
}
func validateCountryReconciliation(ctx context.Context, q ordersQuerier, work orders.Work, c orders.Completion) error {
	if work.Command.Action != orders.ActionCountrySet {
		return nil
	}
	e := c.Receipt.CountryEvidence
	if work.State == orders.StateUnknown || work.Receipt.HTTPStatus != 200 || work.Receipt.CountryEvidence == nil || e == nil || c.ObservedPosting == nil || e.CountryValueObserved || !e.RequirementResolved || e.Scope != work.Scope || e.SKU != work.Command.PostingAction.Parameters.CountrySKU || e.Meaning != "country_requirement_resolution" {
		return orders.ErrStale
	}
	hash, err := orders.PackingTargetFingerprint(*c.ObservedPosting)
	if err != nil || hash != e.TargetFingerprint || hash != work.Command.PostingAction.PackingTargetFingerprint || !orders.CountryRequirementCleared(*c.ObservedPosting, e.SKU) {
		return orders.ErrStale
	}
	var now time.Time
	if err = q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return ordersError("read country evidence clock", err)
	}
	if e.CheckedAt.IsZero() || e.CheckedAt.After(now.Add(time.Minute)) || now.Sub(e.CheckedAt) > 5*time.Minute {
		return orders.ErrStale
	}
	return nil
}

func validateCountryReceiptUpdate(work orders.Work, c orders.Completion) error {
	e := c.Receipt.CountryEvidence
	if work.Command.Action != orders.ActionCountrySet {
		if e != nil {
			return orders.ErrInvalid
		}
		return nil
	}
	if work.State == orders.StateUnknown && c.State != orders.StateUnknown {
		return orders.ErrBlocked
	}
	if work.State.ReadOnly() && c.Receipt.State != "" && c.Receipt.HTTPStatus != work.Receipt.HTTPStatus {
		return orders.ErrStale
	}
	if e == nil {
		if c.State == orders.StateAcknowledged || c.State == orders.StateReconciled {
			return orders.ErrStale
		}
		return nil
	}
	if e.Scope != work.Scope || e.SKU != work.Command.PostingAction.Parameters.CountrySKU || e.TargetFingerprint != work.Command.PostingAction.PackingTargetFingerprint || e.CountryValueObserved || e.Meaning != "country_requirement_resolution" || e.GTDNeeded.Validate() != nil {
		return orders.ErrStale
	}
	previous := work.Receipt.CountryEvidence
	if previous != nil && !reflect.DeepEqual(previous.GTDNeeded, e.GTDNeeded) {
		return orders.ErrStale
	}
	if previous == nil && work.State.ReadOnly() && e.GTDNeeded.IsKnown() {
		return orders.ErrStale
	}
	return nil
}
