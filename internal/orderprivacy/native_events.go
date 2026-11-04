package orderprivacy

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (p ActionProtector) ProtectNativeMovementEvent(ctx context.Context, config orders.Config, proposal orders.NativeMovementProposal, now time.Time) (orders.NativeMovementEvent, error) {
	reference := strings.TrimSpace(proposal.EventReference)
	if !proposal.Action.IsNativeMovement() || reference == "" || len(reference) > 4096 || p.Vault == nil {
		return orders.NativeMovementEvent{}, orders.ErrInvalid
	}
	occurred := proposal.EventOccurredAt
	if !proposal.Applicable.IsKnown() {
		return orders.NativeMovementEvent{}, orders.ErrInvalid
	}
	if !*proposal.Applicable.Value {
		occurred = now
	}
	input := orders.ActionParameters{EventReference: reference, EventConfirmed: proposal.EventConfirmed, NativeEventKind: proposal.EventKind, NativeEventOccurredAt: occurred}
	_, protected, err := p.ProtectOrderActionInput(ctx, config, proposal.PostingNumber, proposal.Action, input, now)
	if err != nil || protected == nil {
		return orders.NativeMovementEvent{}, orders.ErrBlocked
	}
	digest, err := p.nativeEventDigest(config.Scope, proposal.Action, reference)
	if err != nil {
		return orders.NativeMovementEvent{}, err
	}
	event := orders.NativeMovementEvent{Kind: proposal.EventKind, OccurredAt: occurred, Confirmed: proposal.EventConfirmed, Digest: digest, ProtectedInput: *protected}
	if err = event.Validate(config.Revision, *proposal.Applicable.Value); err != nil {
		return orders.NativeMovementEvent{}, err
	}
	return event, nil
}

func (p ActionProtector) nativeEventDigest(scope orders.Scope, action orders.Action, reference string) (string, error) {
	// Deliberately omit Ozon account/posting: the same event must not move the
	// same MoySklad inventory twice through two marketplace projections.
	raw, err := json.Marshal(struct{ Tenant, Account, Kind, Reference string }{scope.Tenant, scope.MoyskladAccount, action.NativeDocumentKind(), reference})
	if err != nil || p.Vault == nil {
		return "", orders.ErrInvalid
	}
	return hex.EncodeToString(p.Vault.Digest(raw)), nil
}

func (p ActionProtector) VerifyNativeMovementEvent(ctx context.Context, command orders.Command) error {
	movement := command.NativeMovement
	if movement == nil {
		return orders.ErrInvalid
	}
	input, err := p.ReadOrderActionInput(ctx, command.Scope, command.PostingNumber, command.Action, movement.Event.ProtectedInput)
	if err != nil {
		return err
	}
	digest, err := p.nativeEventDigest(command.Scope, command.Action, input.EventReference)
	if err != nil || digest != movement.Event.Digest || input.EventReference == "" || input.NativeEventKind != movement.Event.Kind || input.EventConfirmed != movement.Event.Confirmed || !input.NativeEventOccurredAt.Equal(movement.Event.OccurredAt) {
		return orders.ErrStale
	}
	return nil
}
