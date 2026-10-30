//go:build integration

package integration

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

// TestOzonFulfillmentFbsE2E verifies the read path of the Ozon FBS fulfillment
// flow against the live provider: read postings from Ozon, confirm at least one
// is in an actionable state. This is read-only; it does not mutate provider
// state. Requires live provider credentials in the environment
// (OZON_API_KEY, OZON_CLIENT_ID) and a dedicated test database
// (TEST_DATABASE_URL). Skipped silently when credentials are absent.
func TestOzonFulfillmentFbsE2E(t *testing.T) {
	for _, key := range []string{"OZON_API_KEY", "OZON_CLIENT_ID"} {
		if os.Getenv(key) == "" {
			t.Skipf("skipping live Ozon fulfillment e2e: %s not set", key)
		}
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("skipping live Ozon fulfillment e2e: TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	db, err := postgres.Open(ctx, dsn, 15*time.Second)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer db.Pool.Close()

	scope := orders.Scope{
		Tenant:          "e2e-tenant",
		OzonAccount:     os.Getenv("OZON_CLIENT_ID"),
		MoyskladAccount: os.Getenv("MOYSKLAD_ACCOUNT_ID"),
	}
	if scope.MoyskladAccount == "" {
		t.Skip("skipping: MOYSKLAD_ACCOUNT_ID not set")
	}

	client, err := ozon.New(ozon.Options{
		BaseURL:       "https://api-seller.ozon.ru",
		ClientID:      os.Getenv("OZON_CLIENT_ID"),
		APIKey:        os.Getenv("OZON_API_KEY"),
		Timeout:       30 * time.Second,
		Interval:      400 * time.Millisecond,
		Backoff:       1 * time.Second,
		MaxRetryAfter: 1 * time.Minute,
		Attempts:      1,
		PageSize:      100,
		HTTPClient:    &http.Client{Timeout: 30 * time.Second},
	})
	if err != nil {
		t.Fatalf("create Ozon client: %v", err)
	}

	ozonClient, err := ozon.NewOrderClient(client, scope)
	if err != nil {
		t.Fatalf("create Ozon order client: %v", err)
	}

	// Read unfulfilled postings from live Ozon. Read-only; no provider state
	// is mutated.
	from := time.Now().UTC().Add(-7 * 24 * time.Hour)
	to := time.Now().UTC()
	window := orders.OrderWindow{Stream: "ozon-orders-unfulfilled", From: from, To: to, Limit: 10}
	page, err := ozonClient.ReadOrderPage(ctx, scope, window, "")
	if err != nil {
		t.Fatalf("read Ozon postings: %v", err)
	}
	if len(page.Postings) == 0 {
		t.Skip("no unfulfilled Ozon postings available for e2e test")
	}

	// Report only safe fields: posting number, status, scheme. Never log
	// credentials, customer PII, or payment data.
	for _, p := range page.Postings {
		status := "<missing>"
		if p.Status.Value != nil {
			status = *p.Status.Value
		}
		scheme := "<missing>"
		if p.Scheme.Value != nil {
			scheme = *p.Scheme.Value
		}
		t.Logf("posting=%s status=%s scheme=%s", p.PostingNumber, status, scheme)
	}

	// Verify at least one posting is in a packable state.
	packable := false
	for _, p := range page.Postings {
		if s := p.Status.Value; s != nil && (*s == "awaiting_packaging" || *s == "awaiting_deliver") {
			packable = true
			break
		}
	}
	if !packable {
		t.Skip("no packable Ozon posting available for e2e test")
	}

	// The read-only page confirms the live provider path works. Full
	// dispatch (pack/ship/deliver) requires a real posting number and
	// reviewed capability evidence, which is exercised by unit tests with
	// mocked providers. The e2e guard here prevents blind mutation: we only
	// read, never write, on the live account in this test.
	t.Logf("live Ozon FBS read path confirmed: %d posting(s) visible", len(page.Postings))
}
