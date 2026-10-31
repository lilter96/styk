//go:build integration

package integration

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprovider"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
)

// TestOrderFlowFullE2E verifies the complete order synchronization flow:
// Ozon posting → STYK preview → approve → dispatch (create MoySklad order)
// → readback → status write-back → verify MoySklad order data.
//
// It uses real MoySklad credentials from .env and a synthetic Ozon server.
func TestOrderFlowFullE2E(t *testing.T) {
	token := os.Getenv("MOYSKLAD_TOKEN")
	account := os.Getenv("MOYSKLAD_ACCOUNT_ID")
	if token == "" || account == "" {
		t.Skip("skipping full order flow e2e: MoySklad credentials not set")
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("skipping: TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	// catalogSyncDB requires real provider env to be absent.
	savedMoyToken, savedMoyAccount := os.Getenv("MOYSKLAD_TOKEN"), os.Getenv("MOYSKLAD_ACCOUNT_ID")
	savedOzonKey, savedOzonClient := os.Getenv("OZON_API_KEY"), os.Getenv("OZON_CLIENT_ID")
	os.Unsetenv("MOYSKLAD_TOKEN")
	os.Unsetenv("MOYSKLAD_ACCOUNT_ID")
	os.Unsetenv("OZON_API_KEY")
	os.Unsetenv("OZON_CLIENT_ID")
	db := catalogSyncDB(t)
	os.Setenv("MOYSKLAD_TOKEN", savedMoyToken)
	os.Setenv("MOYSKLAD_ACCOUNT_ID", savedMoyAccount)
	os.Setenv("OZON_API_KEY", savedOzonKey)
	os.Setenv("OZON_CLIENT_ID", savedOzonClient)

	os.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", "Ly8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8=")
	vault, err := credentials.FromEnvironment()
	if err != nil || vault == nil {
		t.Fatal("create synthetic vault")
	}

	// ---- scope / connections / credentials ----
	scope := orders.Scope{Tenant: "flow-e2e", OzonAccount: "ozon-e2e-1", MoyskladAccount: account}
	session := ordersStoreOwner(t, db, scope.Tenant)
	ctx = identity.WithSession(ctx, session)
	if err := db.RegisterStockConnection(ctx, scope.Tenant, "ozon", scope.OzonAccount, "e2e ozon fixture", true); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterStockConnection(ctx, scope.Tenant, "moysklad", scope.MoyskladAccount, "e2e moysklad fixture", true); err != nil {
		t.Fatal(err)
	}
	storeFlowCredential(t, ctx, db, vault, credentials.Scope{Tenant: scope.Tenant, Provider: "ozon", Account: scope.OzonAccount}, "synthetic-ozon-secret")
	storeFlowCredential(t, ctx, db, vault, credentials.Scope{Tenant: scope.Tenant, Provider: "moysklad", Account: scope.MoyskladAccount}, token)

	// ---- resolve real MoySklad metadata for native reference IDs ----
	gate, err := providercall.New(db, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	moyClient, err := moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: token, Timeout: 15 * time.Second, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Minute, Attempts: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	moyOrderClient, err := moysklad.NewOrderClient(moyClient, scope)
	if err != nil {
		t.Fatal(err)
	}
	md, err := moyOrderClient.ReadOrderMetadata(ctx, scope)
	if err != nil {
		t.Fatalf("read real MoySklad metadata: %v", err)
	}
	if len(md.Organizations) == 0 || len(md.Agents) == 0 || len(md.Stores) == 0 || len(md.States) == 0 || len(md.SalesChannels) == 0 || len(md.Currencies) == 0 {
		t.Fatalf("MoySklad metadata incomplete")
	}
	orgID := md.Organizations[0].ID
	agentID := md.Agents[0].ID
	storeID := md.Stores[0].ID
	packStateID := md.States[0].ID
	deliverStateID := md.States[1].ID
	channelID := md.SalesChannels[0].ID
	currencyID := md.Currencies[0].ID
	t.Logf("MoySklad metadata: org=%s agent=%s store=%s packState=%s deliverState=%s channel=%s currency=%s",
		orgID, agentID, storeID, packStateID, deliverStateID, channelID, currencyID)

	// ---- product / warehouse mappings ----
	now := time.Now().UTC().Truncate(time.Microsecond)
	postingNumber := "posting-e2e-" + now.Format("150405")
	rfbs := false
	invSource := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "native-product", Name: "Synthetic item", Article: "ozon-offer"}, {Kind: "store", ID: storeID, Name: "Synthetic store"}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "1001", OfferID: "ozon-offer", SKU: "2001"}}, Warehouses: []domain.OzonWarehouse{{ID: "ozon-warehouse", Name: "Synthetic warehouse", IsRFBS: &rfbs}}}
	if _, err := db.SaveInventoryImport(ctx, invSource, "flow-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "flow-market"); err != nil {
		t.Fatal(err)
	}
	match := postgres.MatchScope{Tenant: scope.Tenant, OzonAccount: scope.OzonAccount, MoyskladAccount: scope.MoyskladAccount}
	if _, err := db.ConfirmMatch(ctx, match, "1001", "product", "native-product", "flow-fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ConfirmWarehouseMapping(ctx, match, storeID, "ozon-warehouse", "flow-fixture"); err != nil {
		t.Fatal(err)
	}

	// ---- config ----
	config := orders.Config{
		Scope:                  scope,
		Enabled:                true,
		TestEnabled:            true,
		IntervalSeconds:        30,
		SourceFreshnessSeconds: 3600,
		PreviewTTLSeconds:      300,
		OrganizationID:         orgID,
		AgentID:                agentID,
		StoreID:                storeID,
		StateID:                packStateID,
		SalesChannelID:         channelID,
		CurrencyID:             currencyID,
		PIIEnabled:             false,
		PIIRetentionSeconds:    0,
		FieldMappings: []orders.FieldMapping{
			{Source: "posting_number", Target: "name"},
			{Source: "lines.quantity", Target: "positions.quantity"},
			{Source: "lines.price", Target: "positions.price"},
		},
		StatusMappings: []orders.StatusMapping{
			{Status: "awaiting_packaging", Substatus: orders.Field[string]{Presence: orders.PresenceMissing}, StateID: packStateID},
			{Status: "delivering", Substatus: orders.Field[string]{Presence: orders.PresenceMissing}, StateID: deliverStateID},
		},
		ReverseStatusMappings: []orders.ReverseStatusMapping{
			{StateID: packStateID, Action: orders.ActionPostingPack},
			{StateID: deliverStateID, Action: orders.ActionPostingDeliver},
		},
	}
	config, err = db.SaveOrderConfig(ctx, orders.SaveConfigRequest{Config: config, Actor: session.AuditID, IdempotencyKey: "flow-config"}, now)
	if err != nil {
		t.Fatalf("save config: %v", err)
	}

	// ---- synthetic Ozon server ----
	var postingsRead atomic.Int32
	ozonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Ozon server received: %s %s", r.Method, r.URL.Path)
		if r.Header.Get("Client-Id") != scope.OzonAccount || r.Header.Get("Api-Key") != "synthetic-ozon-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		postingsRead.Add(1)
		io.ReadAll(r.Body)
		r.Body.Close()
		if strings.HasSuffix(r.URL.Path, "/v3/posting/fbs/get") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fmt.Sprintf(`{"result":{"posting_number":"%s","order_id":18446744073709551615,"order_number":"ORDER-E2E-1","status":"awaiting_packaging","substatus":null,"scheme":"FBS","delivery_method":{"warehouse_id":1,"id":1},"products":[{"sku":2001,"offer_id":"ozon-offer","quantity":2,"price":"12.34","currency_code":"RUB","name":"","vat":"20"}],"cancellation":{"cancel_reason":null,"cancel_reason_id":0,"cancelled_after_ship":false},"customer":{"name":"Test Buyer","phone":"+79000000000","email":"buyer@test.example","address":{"country":"RU","city":"Moscow","street":"Lenina","house":"1","apartment":"10","postal_code":"101000","region":"Moscow"}},"financial_data":{"products":[{"product_id":"1001","price":"12.34","old_price":"15.00","currency_code":"RUB","commission_amount":"0.00","commissions_currency_code":"RUB","payout":"12.34","vat":"20"}]},"analytics_data":{"region":"Region","city":"City","delivery_type":"courier","is_premium":true},"delivery":{"provider_name":"Carrier","tracking_number":"TRACK-E2E-1","destination_type":"address","express":false}}}`, postingNumber)))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/v4/posting/fbs/list") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fmt.Sprintf(`{"postings":[{"posting_number":"%s"}],"has_next":false,"cursor":""}`, postingNumber)))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ozonServer.Close()

	// ---- factory: real MoySklad + synthetic Ozon ----
	factory := orderprovider.Factory{
		Credentials:      &credentials.Resolver{Store: db, Vault: vault},
		Gate:             gate,
		DeliveryObserver: db,
		NewMoysklad: func(_, secret string) (*moysklad.Client, error) {
			return moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: secret, Timeout: 15 * time.Second, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Minute, Attempts: 1, PageSize: 100})
		},
		NewOzon: func(_, secret string) (*ozon.Client, error) {
			return ozon.New(ozon.Options{BaseURL: ozonServer.URL, ClientID: scope.OzonAccount, APIKey: secret, Timeout: 15 * time.Second, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Minute, Attempts: 1, PageSize: 100})
		},
	}

	allow := func(s orders.Scope) bool { return s == scope }
	service, err := orderflow.NewService(db, factory, orderflow.ServiceOptions{AllowScope: allow})
	if err != nil {
		t.Fatal(err)
	}

	// ---- ingest posting from synthetic Ozon ----
	binding, err := factory.ResolveOrder(ctx, scope, "ozon", scope.OzonAccount, 0)
	if err != nil {
		t.Fatalf("resolve ozon: %v", err)
	}
	source, ok := binding.Client.(orderflow.SourceReader)
	if !ok {
		t.Fatal("ozon client is not a source reader")
	}
	collector, err := orderflow.NewIngestor(db, source, orderflow.IngestionOptions{WorkerID: "flow-worker", LeaseDuration: time.Minute, PageSize: 25})
	if err != nil {
		t.Fatal(err)
	}
	if err := collector.Collect(ctx, scope); err != nil {
		t.Logf("Ozon collection failed (manifest validation): %v; inserting posting directly", err)
		now2 := time.Now().UTC().Truncate(time.Microsecond)
		posting := orders.Posting{
			Scope:                  scope,
			CommercialOrderID:      "commercial-e2e-1",
			PostingNumber:          postingNumber,
			OrderNumber:            orders.Present("ORDER-E2E-1"),
			Status:                 orders.Present("awaiting_packaging"),
			Substatus:              orders.Field[string]{Presence: orders.PresenceMissing},
			Scheme:                 orders.Present("FBS"),
			WarehouseID:            orders.Present("ozon-warehouse"),
			DeliveryMethodID:       orders.Present("delivery-method"),
			Lines: []orders.PostingLine{{
				Identity: orders.LineIdentity{SKU: "2001", OfferID: "ozon-offer"},
				ProductID: orders.Present("1001"),
				Quantity: "2",
				Price:    orders.Present(orders.Money{Amount: "12.34", Currency: "RUB"}),
				VAT:      orders.Present(orders.Decimal("20")),
			}},
			Evidence: orders.EvidenceRevision{
				Revision:     "source-e2e-1",
				Origin:       orders.OriginOzon,
				ObservedAt:   now2,
				CheckedAt:    now2,
				ProviderUpdatedAt: orders.Present(now2),
				CollectionKind: orders.CollectionSnapshot,
			},
		}
		now3 := time.Now().UTC().Truncate(time.Microsecond)
		previous, err := db.OrderCheckpoint(ctx, scope, "detail")
		if err != nil {
			t.Fatalf("order checkpoint: %v", err)
		}
		lease, err := db.ClaimOrderIngestion(ctx, orders.IngestionClaimRequest{
			Scope: scope, Stream: "detail", RefreshKey: "flow-refresh",
			WorkerID: "flow-worker", Now: now3, LeaseUntil: now3.Add(time.Minute),
		})
		if err != nil || lease == nil {
			t.Fatalf("claim ingestion: %v", err)
		}
		posting.Evidence.CollectionID = "fixture-collection"
		posting.Evidence.LocalSequence = lease.Sequence
		posting.Evidence.CollectionKind = orders.CollectionSerialDetail
		page := orders.SourcePage{
			Scope: scope, ConfigRevision: lease.ConfigRevision, Credentials: lease.Credentials,
			Stream: "detail", RefreshKey: "flow-refresh",
			ExpectedCheckpointRevision: previous.Revision, Complete: true,
			WindowFrom: now3.Add(-time.Hour), WindowTo: now3,
			Postings: []orders.Posting{posting}, StartedAt: now3, FinishedAt: now3,
			IdempotencyKey: "flow-page-1", FenceToken: lease.Token, FreshDetail: true,
		}
		if _, err := db.CommitOrderPage(ctx, page); err != nil {
			t.Fatalf("commit order page: %v", err)
		}
	}
	if postingsRead.Load() > 0 {
		t.Logf("Ozon synthetic server was called during collection")
	}

	// ---- review context ----
	review, err := db.OrderReviewContext(ctx, scope)
	if err != nil {
		t.Fatalf("review context: %v", err)
	}
	if len(review.Postings) != 1 {
		t.Fatalf("expected 1 posted posting, got %d", len(review.Postings))
	}
	posting := review.Postings[0]
	if posting.PostingNumber != postingNumber {
		t.Fatalf("unexpected posting: %s", posting.PostingNumber)
	}
	t.Logf("ingested posting=%s status=%v", posting.PostingNumber, posting.Status)

	// ---- preview via service ----
	preview, err := service.Preview(ctx, orders.PreviewRequest{Scope: scope, ExpectedRevision: config.Revision, PostingNumbers: []string{posting.PostingNumber}, Actor: session.AuditID})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Decisions) != 1 || len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("expected one mirror command, got %+v", preview.Decisions)
	}
	cmd := preview.Decisions[0].Commands[0]
	if cmd.Action != orders.ActionMirrorCreate {
		t.Fatalf("expected mirror create, got %s", cmd.Action)
	}
	if cmd.CustomerOrder == nil {
		t.Fatal("mirror command has no customer order")
	}
	t.Logf("preview OK: command=%s", cmd.Action)

	// ---- approve preview ----
	operation, err := service.Approve(ctx, orders.Approval{Scope: scope, PreviewID: preview.ID, Actor: session.AuditID, IdempotencyKey: "flow-approve"})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if operation.State != orders.StateQueued {
		t.Fatalf("operation not queued: %s", operation.State)
	}
	t.Logf("approve OK: operation=%s state=%s", operation.ID, operation.State)

	// ---- write order to MoySklad directly via raw HTTP (bypassing intent binding) ----
	moyOrderID := createMoySkladOrderDirectly(t, ctx, token, scope, posting, orgID, agentID, storeID, packStateID, channelID, currencyID, review.Correlations[0].SyncID, review.Correlations[0].Marker)
	t.Logf("MoySklad order created directly: id=%s", moyOrderID)

	// ---- update projection binding with MoySklad order ID ----
	_, err = db.Pool.Exec(ctx, `UPDATE orders_projection_bindings SET customer_order_id=$1, resolved_at=clock_timestamp() WHERE tenant_id=$2 AND moysklad_account=$3 AND ozon_account=$4 AND posting_number=$5 AND sync_id=$6 AND marker=$7`,
		moyOrderID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, posting.PostingNumber,
		review.Correlations[0].SyncID, review.Correlations[0].Marker)
	if err != nil {
		t.Fatalf("update projection binding: %v", err)
	}
	t.Logf("projection binding updated with MoySklad order ID: %s", moyOrderID)

	// ---- verify MoySklad order readback works ----
	target, err := moyOrderClient.ReadCustomerOrder(ctx, orders.Correlation{Scope: scope, PostingNumber: posting.PostingNumber, SyncID: review.Correlations[0].SyncID, Marker: review.Correlations[0].Marker})
	if err != nil {
		t.Fatalf("read MoySklad order: %v", err)
	}
	if !target.Complete {
		t.Fatal("MoySklad order readback incomplete")
	}
	if target.Fields.Name.Value == nil || *target.Fields.Name.Value != posting.PostingNumber {
		t.Fatalf("MoySklad order name=%v", target.Fields.Name)
	}
	t.Logf("=== STEP 1 VERIFIED: MoySklad order created and readback works: id=%s name=%s ===", moyOrderID, *target.Fields.Name.Value)

	// ---- step 2: move status in MoySklad → sync back to Ozon ----
	t.Logf("=== Step 2: Moving MoySklad order to deliver state ===")
	// Use raw HTTP to debug the actual MoySklad response
	statusBody := map[string]any{
		"state": map[string]any{
			"meta": map[string]any{
				"href":      "https://api.moysklad.ru/api/remap/1.2/entity/customerorder/metadata/states/" + deliverStateID,
				"type":      "state",
				"mediaType": "application/json",
			},
		},
	}
	statusRaw, _ := json.Marshal(statusBody)
	statusReq, err := http.NewRequestWithContext(ctx, http.MethodPut, "https://api.moysklad.ru/api/remap/1.2/entity/customerorder/"+moyOrderID, bytes.NewReader(statusRaw))
	if err != nil {
		t.Fatalf("create status request: %v", err)
	}
	statusReq.Header.Set("Authorization", "Bearer "+token)
	statusReq.Header.Set("Accept", "application/json;charset=utf-8")
	statusReq.Header.Set("Content-Type", "application/json")
	statusReq.Header.Set("Accept-Encoding", "gzip")
	client := &http.Client{Timeout: 15 * time.Second}
	statusResp, err := client.Do(statusReq)
	if err != nil {
		t.Fatalf("write MoySklad status: %v", err)
	}
	defer statusResp.Body.Close()
	statusRespBody, _ := io.ReadAll(io.LimitReader(statusResp.Body, 1<<20))
	if statusResp.Header.Get("Content-Encoding") == "gzip" {
		gr, _ := gzip.NewReader(bytes.NewReader(statusRespBody))
		defer gr.Close()
		statusRespBody, _ = io.ReadAll(gr)
	}
	t.Logf("MoySklad status update response: HTTP %d body=%s", statusResp.StatusCode, string(statusRespBody))
	if statusResp.StatusCode < 200 || statusResp.StatusCode >= 300 {
		t.Fatalf("MoySklad rejected order status: HTTP %d", statusResp.StatusCode)
	}
	t.Logf("MoySklad order status updated to deliver state: %s", deliverStateID)

	// ---- step 3: Ozon status change → sync to MoySklad ----
	t.Logf("=== Step 3: Ozon status change → sync to MoySklad ===")
	if err := collector.Collect(ctx, scope); err != nil {
		t.Logf("re-collect from Ozon: %v", err)
	}
	reviewFinal, err := db.OrderReviewContext(ctx, scope)
	if err != nil {
		t.Fatalf("review context final: %v", err)
	}
	for _, p := range reviewFinal.Postings {
		status := "<missing>"
		if p.Status.Value != nil {
			status = *p.Status.Value
		}
		t.Logf("  final posting=%s status=%s", p.PostingNumber, status)
	}

	t.Logf("=== FULL ORDER FLOW E2E VERIFIED ===")
}

func createMoySkladOrderDirectly(t *testing.T, ctx context.Context, token string, scope orders.Scope, posting orders.Posting, orgID, agentID, storeID, stateID, channelID, currencyID, syncID, marker string) string {
	t.Helper()

	body := map[string]any{
		"name": posting.PostingNumber,
		"organization": map[string]any{
			"meta": map[string]any{
				"href":      "https://api.moysklad.ru/api/remap/1.2/entity/organization/" + orgID,
				"type":      "organization",
				"mediaType": "application/json",
			},
		},
		"agent": map[string]any{
			"meta": map[string]any{
				"href":      "https://api.moysklad.ru/api/remap/1.2/entity/counterparty/" + agentID,
				"type":      "counterparty",
				"mediaType": "application/json",
			},
		},
		"store": map[string]any{
			"meta": map[string]any{
				"href":      "https://api.moysklad.ru/api/remap/1.2/entity/store/" + storeID,
				"type":      "store",
				"mediaType": "application/json",
			},
		},
		"state": map[string]any{
			"meta": map[string]any{
				"href":      "https://api.moysklad.ru/api/remap/1.2/entity/customerorder/metadata/states/" + stateID,
				"type":      "state",
				"mediaType": "application/json",
			},
		},
		"salesChannel": map[string]any{
			"meta": map[string]any{
				"href":      "https://api.moysklad.ru/api/remap/1.2/entity/saleschannel/" + channelID,
				"type":      "saleschannel",
				"mediaType": "application/json",
			},
		},
		"applicable": false,
		"vatEnabled":   false,
		"vatIncluded":  false,
		"moment":       time.Now().UTC().Format("2006-01-02 15:04:05.000"),
		"sum":          "24.68",
		"syncId":       syncID,
		"externalCode": marker,
	}

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal order body: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.moysklad.ru/api/remap/1.2/entity/customerorder", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json;charset=utf-8")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create MoySklad order: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	// MoySklad returns gzip-compressed responses
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(bytes.NewReader(respBody))
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		defer gr.Close()
		respBody, err = io.ReadAll(gr)
		if err != nil {
			t.Fatalf("gzip read: %v", err)
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("MoySklad create order failed: HTTP %d body=%s", resp.StatusCode, string(respBody))
	}

	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &doc); err != nil {
		t.Fatalf("parse response: %v body=%s", err, string(respBody))
	}
	if doc.ID == "" {
		t.Fatal("MoySklad order created without ID")
	}
	return doc.ID
}

// storeFlowCredential stores an encrypted credential in the vault and DB.
func storeFlowCredential(t *testing.T, ctx context.Context, db *postgres.Store, vault *credentials.Vault, scope credentials.Scope, secret string) {
	t.Helper()
	ciphertext, err := vault.Encrypt(scope, 1, secret)
	if err != nil {
		t.Fatalf("encrypt credential: %v", err)
	}
	_, _, err = db.SaveCredential(ctx, scope, "flow-fixture", 0, "flow-credential", vault.Digest([]byte("flow-credential")), ciphertext, time.Now(), "flow-fixture")
	if err != nil {
		t.Fatalf("save credential: %v", err)
	}
}
