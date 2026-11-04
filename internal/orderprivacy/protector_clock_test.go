package orderprivacy

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

func TestCustomerSecretClockAlignsWithoutChangingSourceObservation(t *testing.T) {
	t.Parallel()
	vault, err := credentials.New(bytes.Repeat([]byte{48}, 32))
	if err != nil {
		t.Fatal(err)
	}
	scope := orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}
	now := time.Date(2026, 11, 4, 12, 0, 0, 123456789, time.FixedZone("source-zone", 3*3600))
	posting := orders.Posting{Scope: scope, CommercialOrderID: "order", PostingNumber: "posting", Status: orders.Present("cancelled"), Evidence: orders.EvidenceRevision{Revision: "v1", Origin: orders.OriginOzon, ObservedAt: now}, PII: &orders.Customer{Name: orders.Present("SYNTHETIC")}, Lines: []orders.PostingLine{{Identity: orders.LineIdentity{SKU: "100"}, Quantity: "1"}}}
	page := orders.SourcePage{Scope: scope, FinishedAt: now, Postings: []orders.Posting{posting}}
	p := Protector{Vault: vault, Store: &secretMemory{}}
	if err = p.ProtectOrderPage(context.Background(), orders.Config{Scope: scope, PIIEnabled: true, PIIRetentionSeconds: 3600}, &page); err != nil {
		t.Fatal(err)
	}
	if !page.FinishedAt.Equal(now) || !page.Postings[0].Evidence.ObservedAt.Equal(now) {
		t.Fatal("provider/source observation timestamp overwritten")
	}
	if len(page.Secrets) != 1 || page.Secrets[0].CreatedAt.Nanosecond()%1000 != 0 || page.Secrets[0].ExpiresAt.Nanosecond()%1000 != 0 || page.Secrets[0].CreatedAt.Location() != time.UTC {
		t.Fatal("local retention clock incompatible with PostgreSQL")
	}
}
