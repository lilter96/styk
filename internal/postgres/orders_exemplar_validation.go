package postgres

import (
	"context"
	"reflect"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func validateOrderExemplarCommand(ctx context.Context, q ordersQuerier, posting orders.Posting, command orders.Command) error {
	if !command.Action.IsExemplar() {
		return nil
	}
	if !posting.Status.IsKnown() || *posting.Status.Value != "awaiting_packaging" {
		return orders.ErrStale
	}
	hash, err := orders.PackingTargetFingerprint(posting)
	if err != nil || command.PostingAction.PackingTargetFingerprint != hash {
		return orders.ErrStale
	}
	if command.Action == orders.ActionExemplarPrepare {
		return nil
	}
	s, err := readOrderExemplarSecret(ctx, q, command.Scope, command.PostingAction.Parameters.ExemplarWorkID)
	if err != nil {
		return err
	}
	if !s.Ready || s.PostingNumber != posting.PostingNumber || s.TargetFingerprint != hash || s.CredentialRevision != command.Credentials.Ozon || !command.PostingAction.Parameters.ExemplarsProvided {
		return orders.ErrStale
	}
	return nil
}

func validateOrderPreviewExemplars(ctx context.Context, q ordersQuerier, preview orders.Preview, current orders.ReviewContext) error {
	for _, d := range preview.Decisions {
		for _, c := range d.Commands {
			if err := validateOrderExemplarCommand(ctx, q, d.Posting, c); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateExemplarReconciliation(ctx context.Context, q ordersQuerier, work orders.Work, c orders.Completion) error {
	if !work.Command.Action.IsExemplar() {
		return nil
	}
	e := c.Receipt.ExemplarEvidence
	if e == nil || !e.Verified || e.CredentialRevision != work.Credentials.Ozon || e.Scope != work.Scope || e.SnapshotID != work.ID || c.ObservedPosting == nil {
		return orders.ErrStale
	}
	s, err := readOrderExemplarSecret(ctx, q, work.Scope, work.ID)
	if err != nil {
		return err
	}
	hash, err := orders.PackingTargetFingerprint(*c.ObservedPosting)
	if err != nil || hash != s.TargetFingerprint || hash != e.TargetFingerprint || e.RequirementsHash != orders.ExemplarRequirementsHash(*c.ObservedPosting) {
		return orders.ErrStale
	}
	want := make(map[string][]string, len(s.Public.Products))
	for _, p := range s.Public.Products {
		want[p.SKU] = p.IDs
	}
	if !reflect.DeepEqual(e.IDs, want) {
		return orders.ErrStale
	}
	if work.Command.Action == orders.ActionExemplarSet && e.Status != "ship_available" {
		return orders.ErrStale
	}
	if work.Command.Action == orders.ActionExemplarPrepare && e.Status != "ship_available" && e.Status != "update_available" {
		return orders.ErrStale
	}
	var now time.Time
	if err = q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return ordersError("read exemplar validation clock", err)
	}
	if e.CheckedAt.IsZero() || e.CheckedAt.After(now.Add(time.Minute)) || now.Sub(e.CheckedAt) > 5*time.Minute {
		return orders.ErrStale
	}
	return nil
}
