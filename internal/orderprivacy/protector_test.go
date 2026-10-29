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

type secretMemory struct{ secret *orders.PostingSecret }

func (s *secretMemory) SaveOrderSecret(_ context.Context, v orders.PostingSecret) error {
	s.secret = &v
	return nil
}
func (s *secretMemory) OrderSecret(_ context.Context, scope orders.Scope, posting string, revision int64) (orders.PostingSecret, error) {
	if s.secret == nil || s.secret.Scope != scope || s.secret.PostingNumber != posting || s.secret.Revision != revision {
		return orders.PostingSecret{}, orders.ErrNotFound
	}
	return *s.secret, nil
}
func (*secretMemory) DeleteExpiredOrderSecrets(context.Context, time.Time, int) (int, error) {
	return 0, nil
}
func TestCustomerProtectionPreservesCiphertextAndRejectsDestinationSubstitution(t *testing.T) {
	vault, err := credentials.New(bytes.Repeat([]byte{17}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &secretMemory{}
	protector := Protector{Vault: vault, Store: store}
	scope := orders.Scope{Tenant: "merchant-a", OzonAccount: "seller-a", MoyskladAccount: "source-a"}
	now := time.Now().UTC()
	posting := orders.Posting{Scope: scope, CommercialOrderID: "order-1", PostingNumber: "posting-1", Status: orders.Present("cancelled"), Substatus: orders.Present("cancelled"), Evidence: orders.EvidenceRevision{Revision: "revision-1", Origin: orders.OriginOzon, ObservedAt: now}, PII: &orders.Customer{Name: orders.Present("Synthetic private receiver")}, Lines: []orders.PostingLine{{Identity: orders.LineIdentity{SKU: "100"}, Quantity: "1"}}}
	config := orders.Config{Scope: scope, PIIEnabled: true, PIIRetentionSeconds: 3600}
	page := orders.SourcePage{Scope: scope, FinishedAt: now, Postings: []orders.Posting{posting}}
	if err = protector.ProtectOrderPage(context.Background(), config, &page); err != nil {
		t.Fatal(err)
	}
	if page.Postings[0].PII != nil || len(page.Secrets) != 1 {
		t.Fatal("customer was not protected")
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("Synthetic private receiver")) {
		t.Fatal("customer leaked into page JSON")
	}
	if err = store.SaveOrderSecret(context.Background(), page.Secrets[0]); err != nil {
		t.Fatal(err)
	}
	customer, err := protector.Customer(context.Background(), scope, "posting-1", page.Postings[0].Customer)
	if err != nil || customer.Name.Value == nil || *customer.Name.Value != "Synthetic private receiver" {
		t.Fatal("authorized customer unavailable")
	}
	previous := page.Postings[0].Customer
	repeated := orders.SourcePage{Scope: scope, FinishedAt: now, Postings: []orders.Posting{posting}}
	if err = protector.ProtectOrderPage(context.Background(), config, &repeated); err != nil {
		t.Fatal(err)
	}
	if len(repeated.Secrets) != 0 || repeated.Postings[0].Customer != previous {
		t.Fatal("unchanged customer created duplicate protection")
	}
	substituted := scope
	substituted.MoyskladAccount = "source-b"
	if _, err = protector.Customer(context.Background(), substituted, "posting-1", previous); err == nil {
		t.Fatal("destination account substitution accepted")
	}
}
