package orderprivacy

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

type actionSecretMemory struct{ secret orders.ActionSecret }

func (s *actionSecretMemory) SaveOrderActionSecret(_ context.Context, secret orders.ActionSecret) (orders.ActionSecret, error) {
	s.secret = secret
	return secret, nil
}

func (s *actionSecretMemory) OrderActionSecret(_ context.Context, scope orders.Scope, posting string, action orders.Action, ref orders.ProtectedActionInput) (orders.ActionSecret, error) {
	if s.secret.Scope != scope || s.secret.PostingNumber != posting || s.secret.Action != action || s.secret.Reference != ref.Reference || s.secret.ConfigRevision != ref.Revision {
		return orders.ActionSecret{}, orders.ErrNotFound
	}
	return s.secret, nil
}

func TestActionInputProtectedAndBoundToExactTarget(t *testing.T) {
	t.Parallel()
	vault, err := credentials.New(bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &actionSecretMemory{}
	protector := ActionProtector{Vault: vault, Store: store}
	scope := orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}
	input := orders.ActionParameters{TrackingNumber: "private-track", EventReference: "private-event"}
	public, ref, err := protector.ProtectOrderActionInput(context.Background(), orders.Config{Scope: scope, Revision: 1}, "posting", orders.ActionPostingTracking, input, time.Now().UTC())
	if err != nil || ref == nil {
		t.Fatalf("protect: %v", err)
	}
	raw, err := json.Marshal(public)
	if err != nil || bytes.Contains(raw, []byte("private")) || bytes.Contains(store.secret.Ciphertext, []byte("private")) {
		t.Fatal("private value not protected")
	}
	actual, err := protector.ReadOrderActionInput(context.Background(), scope, "posting", orders.ActionPostingTracking, *ref)
	if err != nil || actual.TrackingNumber != input.TrackingNumber {
		t.Fatalf("read exact input: %v", err)
	}
	substituted := scope
	substituted.MoyskladAccount = "different"
	if _, err = protector.ReadOrderActionInput(context.Background(), substituted, "posting", orders.ActionPostingTracking, *ref); err == nil {
		t.Fatal("cross-account input accepted")
	}
	if _, err = protector.ReadOrderActionInput(context.Background(), scope, "other-posting", orders.ActionPostingTracking, *ref); err == nil {
		t.Fatal("cross-posting input accepted")
	}
}
