//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogprovider"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/testpublication"
)

const catalogBrowserOrigin = "http://127.0.0.1:15180"
const catalogBrowserSourceName = "Название из МойСклад"
const catalogBrowserOzonDescription = "Описание из Ozon для смешанного направления"
const catalogBrowserUnmappedID = "8080000001"
const catalogBrowserUnmappedOffer = "CATALOG-UNMAPPED"
const catalogBrowserUnmappedName = "Новый товар Ozon без соответствия"
const catalogBrowserUnmappedDescription = "Описание нового товара из Ozon"
const catalogBrowserCreatedID = "11111111-1111-4111-8111-111111111111"

// The browser calls the real merchant API and worker, using real catalog
// adapters against deterministic loopback JSON and an isolated PostgreSQL schema.
func TestCatalogMerchantBrowserStack(t *testing.T) {
	if os.Getenv("STYK_CATALOG_BROWSER_E2E") != "1" {
		t.Skip("catalog browser stack not requested")
	}
	db, _ := controlledFixture(t)
	vault := catalogBrowserVault(t, db)
	seedCatalogBrowserNames(t, db)
	simulator := &catalogBrowserProvider{t: t, moyName: catalogBrowserSourceName,
		moyDescription: "Старое описание МойСклад", ozonName: "Старое название Ozon", ozonDescription: catalogBrowserOzonDescription}
	provider := httptest.NewServer(simulator)
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	stop := startCatalogBrowserWorker(t, ctx, db, vault, provider.URL)
	defer stop()
	mailbox := &testMailbox{tokens: map[string]string{}}
	handler, err := console.MerchantLoopbackTestHandler(db, catalogBrowserOrigin, mailbox, provider.URL, provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := merchantBrowserAPI(t, db, mailbox, catalogBrowserTestRoutes(handler, simulator))
	defer api.Close()
	runCatalogBrowserPlaywright(t, ctx, api.URL)
	assertCatalogBrowserEffects(t, db, simulator)
}

func catalogBrowserTestRoutes(handler http.Handler, simulator *catalogBrowserProvider) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__test/catalog-state", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Secret") != "synthetic-browser-harness" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		simulator.mu.Lock()
		defer simulator.mu.Unlock()
		simulator.encode(w, map[string]any{"moy_writes": simulator.moyWrites, "ozon_writes": simulator.ozonWrites,
			"polls": simulator.polls, "ozon_name": simulator.ozonName, "moy_description": simulator.moyDescription,
			"moy_creates": simulator.moyCreates, "marker_lookups": simulator.markerLookups, "created_readbacks": simulator.createdReadbacks})
	})
	mux.Handle("/", handler)
	return mux
}

func catalogBrowserVault(t *testing.T, db *postgres.Store) *credentials.Vault {
	t.Helper()
	for _, key := range []string{"MOYSKLAD_TOKEN", "MOYSKLAD_ACCOUNT_ID", "OZON_API_KEY", "OZON_CLIENT_ID", "STYK_TEST_ACCOUNT_PUBLICATION", "STYK_TEST_ACCOUNT_CATALOG", "STYK_STOCK_PUBLICATION_ENABLED", "STYK_STOCK_PUBLICATION_TEST_ENDPOINT"} {
		t.Setenv(key, "")
	}
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{21}, 32)))
	vault, err := credentials.FromEnvironment()
	if err != nil || vault == nil {
		t.Fatal("synthetic catalog credential vault unavailable")
	}
	seedMerchantBrowserCredentials(t, db, vault)
	return vault
}

func seedCatalogBrowserNames(t *testing.T, db *postgres.Store) {
	t.Helper()
	now := time.Now().UTC()
	scope := testpublication.Scope()
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now,
		Entities: []domain.ExternalEntity{{Kind: "product", ID: testpublication.SourceProduct, Name: catalogBrowserSourceName, Article: "catalog-browser"}}}
	if _, err := db.SaveInventoryImport(context.Background(), source, "catalog-browser-source"); err != nil {
		t.Fatal(err)
	}
	archived := false
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now.Add(-time.Second), FinishedAt: now,
		Products: []domain.OzonProduct{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, Name: "Старое название Ozon", Archived: &archived},
			{ProductID: catalogBrowserUnmappedID, OfferID: catalogBrowserUnmappedOffer, SKU: "8080000002", Name: catalogBrowserUnmappedName, Archived: &archived}}}
	if _, err := db.SaveOzonImport(context.Background(), market, "catalog-browser-market"); err != nil {
		t.Fatal(err)
	}
}

func catalogBrowserFactory(db *postgres.Store, vault *credentials.Vault, base string) catalogprovider.Factory {
	return catalogprovider.Factory{Credentials: &credentials.Resolver{Store: db, Vault: vault},
		NewMoysklad: func(_, secret string) (*moysklad.Client, error) {
			return moysklad.New(moysklad.Options{BaseURL: base, Token: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
		}, NewOzon: func(account, secret string) (*ozon.Client, error) {
			return ozon.New(ozon.Options{BaseURL: base, ClientID: account, APIKey: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, AllowSyntheticStockWrites: true})
		}}
}

func startCatalogBrowserWorker(t *testing.T, ctx context.Context, db *postgres.Store, vault *credentials.Vault, base string) func() {
	t.Helper()
	scope := catalog.Scope{Tenant: testpublication.Tenant, MoyskladAccount: testpublication.MoyskladAccount, OzonAccount: testpublication.OzonAccount}
	runner := catalogsync.Runner{Enabled: true, WorkerID: "catalog-browser-normal-worker", Store: db,
		Resolver: catalogBrowserFactory(db, vault, base), AllowScope: func(value catalog.Scope) bool { return value == scope }}
	worker := application.Worker{Store: db, Handler: application.MockHandler{}, Catalog: runner, StoredCredentials: true,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 25 * time.Millisecond, Lease: time.Minute, Retry: time.Second}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerCtx) }()
	return func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("stop catalog browser worker: %v", err)
		}
	}
}

func assertCatalogBrowserEffects(t *testing.T, db *postgres.Store, simulator *catalogBrowserProvider) {
	t.Helper()
	simulator.mu.Lock()
	defer simulator.mu.Unlock()
	if simulator.moyWrites != 1 || simulator.ozonWrites != 1 || simulator.polls == 0 {
		t.Fatalf("expected one write per direction and asynchronous task poll: moy=%d ozon=%d polls=%d", simulator.moyWrites, simulator.ozonWrites, simulator.polls)
	}
	if simulator.ozonName != catalogBrowserSourceName || simulator.moyDescription != catalogBrowserOzonDescription {
		t.Fatal("approved mixed field values were not observed in provider readback")
	}
	var reconciled int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM catalog_sync_work WHERE state='RECONCILED' AND tenant_id=$1`, testpublication.Tenant).Scan(&reconciled); err != nil || reconciled != 3 {
		t.Fatalf("normal worker did not durably reconcile mixed updates and creation: count=%d err=%v", reconciled, err)
	}
	assertCatalogBrowserCreation(t, db, simulator)
}
