//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersStructuredAddressApprovalKeepsPIIEncryptedAndScoped(t *testing.T) {
	f := newOrdersStoreFixture(t)
	config := f.config
	config.PIIEnabled, config.PIIRetentionSeconds = true, 3600
	config.FieldMappings = append(config.FieldMappings, orders.FieldMapping{Source: "customer.address.city", Target: "shipment_address_full.city"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "structured-address-config"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	protector := structuredAddressProtector(t, f)
	posting := structuredAddressSource(t, f, protector)
	preview := ordersStorePreview(t, f, posting.PostingNumber)
	preview.ID += "-structured"
	command := &preview.Decisions[0].Commands[0]
	command.ID += "-structured"
	command.IdempotencyKey = command.ID
	command.CustomerOrder.PIIMappings = []orders.PIIAttributeMapping{{Source: "customer.address.city"}}
	command.ProtectedCustomer = &posting.Customer
	customer, err := protector.Customer(f.ctx, f.config.Scope, posting.PostingNumber, posting.Customer)
	if err != nil {
		t.Fatal(err)
	}
	if err = command.CustomerOrder.ApplyCustomerProjection(*customer); err != nil {
		t.Fatal(err)
	}
	preview, err = f.db.SaveOrderPreview(f.ctx, preview)
	if err != nil {
		t.Fatal("reviewed native full mapping rejected", err)
	}
	raw, err := json.Marshal(preview)
	if err != nil || bytes.Contains(raw, []byte("SYNTHETIC-PRIVATE-CITY")) {
		t.Fatal("PII escaped protected projection")
	}
	ordersStoreApprove(t, f, preview)
	work := ordersStoreClaim(t, f, time.Minute)
	allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: work, Now: time.Now()})
	if err != nil || !allowed {
		t.Fatal("native mapping did not pass normal durable worker guard", err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work WHERE work::text LIKE '%SYNTHETIC-PRIVATE-CITY%'`, 0)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_posting_heads WHERE projection::text LIKE '%SYNTHETIC-PRIVATE-CITY%'`, 0)
	foreign := f.config.Scope
	foreign.Tenant = "another-merchant"
	if _, err = f.db.OrderSecret(f.ctx, foreign, posting.PostingNumber, posting.Customer.Revision); !errors.Is(err, orders.ErrNotFound) {
		t.Fatal("cross-tenant protected address accessible", err)
	}
}
func structuredAddressProtector(t *testing.T, f ordersStoreFixture) orderprivacy.Protector {
	t.Helper()
	vault, err := credentials.New(bytes.Repeat([]byte{47}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return orderprivacy.Protector{Vault: vault, Store: f.db}
}
func structuredAddressSource(t *testing.T, f ordersStoreFixture, p orderprivacy.Protector) orders.Posting {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond).Add(123 * time.Nanosecond)
	checkpoint, err := f.db.OrderCheckpoint(f.ctx, f.config.Scope, "detail")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.db.ClaimOrderIngestion(f.ctx, orders.IngestionClaimRequest{Scope: f.config.Scope, Stream: "detail", RefreshKey: "structured-source", WorkerID: "structured-fixture", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	posting := ordersStorePosting(f.config.Scope, "structured-posting", "initial", now)
	posting.PII = &orders.Customer{Address: orders.Present(orders.Address{City: orders.Present("SYNTHETIC-PRIVATE-CITY")})}
	posting.Evidence.CollectionID = "structured-collection"
	posting.Evidence.LocalSequence = lease.Sequence
	posting.Evidence.CollectionKind = orders.CollectionSerialDetail
	page := orders.SourcePage{Scope: f.config.Scope, ConfigRevision: lease.ConfigRevision, Credentials: lease.Credentials, Stream: "detail", RefreshKey: "structured-source", ExpectedCheckpointRevision: checkpoint.Revision, Complete: true, WindowFrom: now.Add(-time.Hour), WindowTo: now, Postings: []orders.Posting{posting}, StartedAt: now, FinishedAt: now, IdempotencyKey: "structured-source", FenceToken: lease.Token, FreshDetail: true}
	if err = p.ProtectOrderPage(f.ctx, f.config, &page); err != nil {
		t.Fatal(err)
	}
	if !page.Postings[0].Evidence.ObservedAt.Equal(now) || page.Secrets[0].CreatedAt.Nanosecond()%1000 != 0 {
		t.Fatal("source timestamp changed or secret not PostgreSQL-aligned")
	}
	if _, err = f.db.CommitOrderPage(f.ctx, page); err != nil {
		t.Fatal(err)
	}
	return page.Postings[0]
}
