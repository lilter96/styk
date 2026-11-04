package orderprivacy

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
	"testing"
	"time"
)

func TestNativePhysicalEventEncryptedScopedAndDeduplicated(t *testing.T) {
	t.Parallel()
	vault, err := credentials.New(bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &actionSecretMemory{}
	protector := ActionProtector{Vault: vault, Store: store}
	config := orders.Config{Scope: orders.Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}, Revision: 1}
	now := time.Now().UTC()
	proposal := orders.NativeMovementProposal{PostingNumber: "posting", Action: orders.ActionDemandCreate, Applicable: orders.Present(true), EventKind: "shipment_dispatched", EventOccurredAt: now, EventConfirmed: true, EventReference: "actual-private-event"}
	event, err := protector.ProtectNativeMovementEvent(context.Background(), config, proposal, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("actual-private-event")) || bytes.Contains(store.secret.Ciphertext, []byte("actual-private-event")) {
		t.Fatal("physical event leaked")
	}
	command := orders.Command{Scope: config.Scope, PostingNumber: proposal.PostingNumber, Action: proposal.Action, NativeMovement: &orders.NativeMovement{Event: event}}
	if err = protector.VerifyNativeMovementEvent(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	command.Scope.MoyskladAccount = "other"
	if err = protector.VerifyNativeMovementEvent(context.Background(), command); err == nil {
		t.Fatal("cross-account event accepted")
	}
	other := config.Scope
	other.OzonAccount = "other-ozon"
	digest, err := protector.nativeEventDigest(other, proposal.Action, proposal.EventReference)
	if err != nil {
		t.Fatal(err)
	}
	if digest != event.Digest {
		t.Fatal("same physical event could move stock twice through different marketplace projections")
	}
}
