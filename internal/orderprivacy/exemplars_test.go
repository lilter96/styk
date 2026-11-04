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

type exemplarSecretMemory struct{ secret orders.ExemplarSecret }

func (s *exemplarSecretMemory) OrderExemplarSecret(context.Context, orders.Scope, string) (orders.ExemplarSecret, error) {
	return s.secret, nil
}

func TestExemplarSecretsBindExactScopeWorkAndCredentialRevision(t *testing.T) {
	t.Parallel()
	vault, err := credentials.New(bytes.Repeat([]byte{43}, 32))
	if err != nil {
		t.Fatal(err)
	}
	scope := orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}
	work := orders.Work{ID: "work", Scope: scope, Credentials: orders.CredentialRevisions{Ozon: 2}, Command: orders.Command{PostingNumber: "posting", PostingAction: &orders.PostingAction{PackingTargetFingerprint: string(bytes.Repeat([]byte{'a'}, 64))}}}
	snapshot := orders.ExemplarSnapshot{PostingNumber: "posting", Products: []orders.ExemplarProduct{{SKU: "2001", Quantity: 1, Exemplars: []orders.ExemplarInput{{ID: "9007199254740993", Marks: orders.Present([]orders.ExemplarMark{{Type: "mandatory_mark", Value: "SYNTHETIC-SECRET\x1dMARK"}})}}}}}
	store := &exemplarSecretMemory{}
	protector := ExemplarProtector{Vault: vault, Store: store}
	store.secret, err = protector.SealOrderExemplars(work, snapshot, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(store.secret.Public)
	if err != nil || bytes.Contains(raw, []byte("SYNTHETIC")) || bytes.Contains(store.secret.Ciphertext, []byte("SYNTHETIC")) {
		t.Fatal("private marks leaked")
	}
	restored, _, err := protector.ReadOrderExemplars(context.Background(), scope, work.ID)
	if err != nil || !orders.ExemplarValuesEqual(snapshot, restored) {
		t.Fatal("private roundtrip failed", err)
	}
	original := store.secret
	for _, mutate := range []func(*orders.ExemplarSecret){func(s *orders.ExemplarSecret) { s.Scope.Tenant = "other" }, func(s *orders.ExemplarSecret) { s.WorkID = "other" }, func(s *orders.ExemplarSecret) { s.CredentialRevision++ }, func(s *orders.ExemplarSecret) { s.TargetFingerprint = string(bytes.Repeat([]byte{'b'}, 64)) }} {
		store.secret = original
		mutate(&store.secret)
		if _, _, err = protector.ReadOrderExemplars(context.Background(), scope, work.ID); err == nil {
			t.Fatal("substituted exemplar accepted")
		}
	}
}
