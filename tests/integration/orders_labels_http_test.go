//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orderprovider"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
)

const labelHTTPPDF = "%PDF-1.7\nSYNTHETIC_RECEIVER\n%%EOF"

// The source/preview fixtures are synthetic; approval, claims, dispatch guards,
// provider JSON parsing, shared budgets, encryption and download are real paths.
func TestOrdersLabelHTTPDispatchReconcilesAndDownloads(t *testing.T) {
	f := newLabelHTTPFixture(t)
	transport := &labelHTTPTransport{account: f.config.Scope.OzonAccount}
	factory, protector := labelHTTPFactory(t, f, transport)
	preview := labelHTTPPreview(t, f, factory)
	operation := labelHTTPApprove(t, f, preview)
	runner, err := orderflow.NewRunner(f.db, factory, orderflow.RunnerOptions{WorkerID: "label-http-worker", PollInterval: time.Millisecond, AllowScope: func(s orders.Scope) bool { return s == f.config.Scope }, LabelSealer: protector})
	if err != nil {
		t.Fatal(err)
	}
	labelHTTPRunPhase(t, f, runner, orders.StatePolling)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_label_artifacts`, 0)
	labelHTTPRunPhase(t, f, runner, orders.StatePolling)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_label_artifacts`, 0)
	labelHTTPRunPhase(t, f, runner, orders.StateReconciled)
	history, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || len(history.Operations) != 1 || history.Operations[0].ID != operation.ID {
		t.Fatalf("durable operation missing: %v", err)
	}
	work := history.Operations[0].Targets[0]
	if work.Receipt.HTTPStatus != 200 || work.Receipt.TaskID != "99" || !work.Receipt.LabelTasksVerified || work.DispatchCount != 1 {
		t.Fatal("provider acknowledgment or exact dispatch evidence lost")
	}
	assertStoredLabel(t, f, work.ID, protector, []byte(labelHTTPPDF))
	assertLabelHTTPIsolation(t, f, work.ID, []byte(labelHTTPPDF))
	assertOrdersExactIntent(t, f, work)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_label_artifacts`, 1)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_receipts WHERE receipt::text LIKE '%https://%'`, 0)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM provider_call_permits WHERE is_write AND started_at IS NOT NULL`, 1)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM provider_call_permits WHERE NOT is_write AND started_at IS NOT NULL AND operation_key=encode(sha256('orders/label/pdf'::bytea),'hex')`, 1)
	if ran, err := runner.RunNext(f.ctx); err != nil || ran {
		t.Fatalf("terminal label replayed: ran=%v error=%v", ran, err)
	}
	transport.assertCounts(t)
}

func newLabelHTTPFixture(t *testing.T) ordersStoreFixture {
	t.Helper()
	f := ordersStoreFixture{db: catalogSyncDB(t)}
	scope := orders.Scope{Tenant: "orders-tenant", OzonAccount: "orders-ozon", MoyskladAccount: "orders-moy"}
	f.session = ordersStoreOwner(t, f.db, scope.Tenant)
	f.ctx = identity.WithSession(context.Background(), f.session)
	ordersStoreConnections(t, f.db, scope)
	labelHTTPMappings(t, f, scope)
	f.config = orders.Config{Scope: scope, Enabled: true, TestEnabled: true, IntervalSeconds: 30, SourceFreshnessSeconds: 3600, PreviewTTLSeconds: 300, OrganizationID: "native-organization", AgentID: "native-agent", StoreID: "native-store", StateID: "native-state", SalesChannelID: "native-channel", FieldMappings: []orders.FieldMapping{{Source: "posting_number", Target: "name"}, {Source: "lines.quantity", Target: "positions.quantity"}, {Source: "lines.price", Target: "positions.price"}, {Source: "lines.vat", Target: "positions.vat"}}}
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, Actor: f.session.AuditID, IdempotencyKey: "label-http-config"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func labelHTTPMappings(t *testing.T, f ordersStoreFixture, scope orders.Scope) {
	t.Helper()
	now := time.Now().UTC()
	rfbs := false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "native-product", Name: "Synthetic item", Article: "orders-offer"}, {Kind: "store", ID: "native-store", Name: "Synthetic store"}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "1001", OfferID: "orders-offer", SKU: "2001"}}, Warehouses: []domain.OzonWarehouse{{ID: "1020001323428000", Name: "Synthetic FBS warehouse", IsRFBS: &rfbs}}}
	if _, err := f.db.SaveInventoryImport(f.ctx, source, "label-http-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SaveOzonImport(f.ctx, market, "label-http-market"); err != nil {
		t.Fatal(err)
	}
	match := postgres.MatchScope{Tenant: scope.Tenant, OzonAccount: scope.OzonAccount, MoyskladAccount: scope.MoyskladAccount}
	if _, err := f.db.ConfirmMatch(f.ctx, match, "1001", "product", "native-product", "label-http-fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ConfirmWarehouseMapping(f.ctx, match, "native-store", "1020001323428000", "label-http-fixture"); err != nil {
		t.Fatal(err)
	}
}

func labelHTTPFactory(t *testing.T, f ordersStoreFixture, transport *labelHTTPTransport) (orderprovider.Factory, orderprivacy.LabelProtector) {
	t.Helper()
	key := bytes.Repeat([]byte{31}, 32)
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	vault, err := credentials.New(key)
	if err != nil {
		t.Fatal(err)
	}
	scope := credentials.Scope{Tenant: f.config.Scope.Tenant, Provider: "ozon", Account: f.config.Scope.OzonAccount}
	ciphertext, err := vault.Encrypt(scope, 2, "synthetic-label-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(f.ctx, `UPDATE connection_credentials SET ciphertext=$4, revision=2 WHERE tenant_id=$1 AND provider=$2 AND account_id=$3`, scope.Tenant, scope.Provider, scope.Account, ciphertext); err != nil {
		t.Fatal(err)
	}
	gate, err := providercall.New(f.db, providercall.Options{})
	if err != nil {
		t.Fatal(err)
	}
	factory := orderprovider.Factory{Credentials: &credentials.Resolver{Store: f.db, Vault: vault}, Gate: gate, NewOzon: func(account, secret string) (*ozon.Client, error) {
		return ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: account, APIKey: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, HTTPClient: &http.Client{Transport: transport}})
	}}
	return factory, orderprivacy.LabelProtector{Vault: vault}
}

func labelHTTPPreview(t *testing.T, f ordersStoreFixture, factory orderprovider.Factory) orders.Preview {
	t.Helper()
	binding, err := factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 2)
	if err != nil {
		t.Fatal(err)
	}
	posting, err := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, "label-http-posting")
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreCommit(t, f, posting, "label-http-source")
	preview := splitStorePreview(t, f, posting)
	preview.ID += "-label-http"
	command := &preview.Decisions[0].Commands[0]
	command.ID += "-label-http"
	command.IdempotencyKey = command.ID
	command.Action = orders.ActionPostingLabelCreate
	command.PostingAction.Parameters = orders.ActionParameters{LabelType: "big_label"}
	preview, err = f.db.SaveOrderPreview(f.ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func labelHTTPMerchant(t *testing.T, f ordersStoreFixture) (*merchantBrowser, identity.Session) {
	t.Helper()
	unusedProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("approval unexpectedly called provider")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(unusedProvider.Close)
	mailbox := &testMailbox{tokens: map[string]string{}}
	handler, err := console.MerchantLoopbackTestHandler(f.db, "http://127.0.0.1:5173", mailbox, unusedProvider.URL, unusedProvider.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	identityStore, err := identity.New(f.db.Pool, f.db.Timeout)
	if err != nil {
		t.Fatal(err)
	}
	session, token, err := identityStore.CreateSession(f.ctx, f.session.User)
	if err != nil {
		t.Fatal(err)
	}
	owner := newMerchantBrowser(t, server.URL)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	owner.client.Jar.SetCookies(u, []*http.Cookie{{Name: "styk_session", Value: token, Path: "/", HttpOnly: true}})
	owner.csrf = session.CSRF
	return owner, session
}

func labelHTTPApprove(t *testing.T, f ordersStoreFixture, preview orders.Preview) orders.Operation {
	t.Helper()
	owner, session := labelHTTPMerchant(t, f)
	preview.ID += "-browser"
	preview.Actor = session.AuditID
	var err error
	preview, err = f.db.SaveOrderPreview(identity.WithSession(f.ctx, session), preview)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "ozon_account": f.config.Scope.OzonAccount, "moysklad_account": f.config.Scope.MoyskladAccount, "preview_id": preview.ID, "idempotency_key": "label-http-approve", "acknowledge": true})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := owner.request(t, http.MethodPost, "/api/orders/approve", string(body), true)
	var operation orders.Operation
	if status != http.StatusOK || json.Unmarshal(raw, &operation) != nil || operation.ID == "" {
		t.Fatalf("merchant approval failed: HTTP %d", status)
	}
	status, raw = owner.request(t, http.MethodPost, "/api/orders/approve", string(body), true)
	var repeated orders.Operation
	if status != http.StatusOK || json.Unmarshal(raw, &repeated) != nil || repeated.ID != operation.ID {
		t.Fatal("duplicate merchant confirmation created another operation")
	}
	return operation
}

func labelHTTPRunPhase(t *testing.T, f ordersStoreFixture, runner *orderflow.Runner, expected orders.State) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	for {
		ran, err := runner.RunNext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ran {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("durable label phase was never claimed")
		case <-time.After(5 * time.Millisecond):
		}
	}
	history, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || len(history.Operations) != 1 || history.Operations[0].State != expected {
		t.Fatalf("label phase expected %s: %+v error=%v", expected, history, err)
	}
}

type labelHTTPTransport struct {
	account              string
	mu                   sync.Mutex
	creates, polls, pdfs int
	details, discoveries int
}

func (s *labelHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.URL.Scheme != "https" || request.URL.Host != "api-seller.ozon.ru" {
		return nil, fmt.Errorf("synthetic label transport refused unexpected origin")
	}
	if request.Method == http.MethodGet {
		if request.URL.Path != "/labels/synthetic.pdf" || request.Header.Get("Api-Key") != "" || request.Header.Get("Client-Id") != "" {
			return nil, fmt.Errorf("PDF request forwarded credentials or wrong target")
		}
		s.pdfs++
		return labelHTTPResponse(request, "application/pdf", labelHTTPPDF), nil
	}
	if request.Method != http.MethodPost || request.Header.Get("Api-Key") != "synthetic-label-secret" || request.Header.Get("Client-Id") != s.account {
		return nil, fmt.Errorf("synthetic provider credentials differ from owned connection")
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	response, err := s.response(request.URL.Path, body)
	if err != nil {
		return nil, err
	}
	return labelHTTPResponse(request, "application/json", response), nil
}

func (s *labelHTTPTransport) response(path string, body []byte) (string, error) {
	switch path {
	case "/v3/posting/fbs/get":
		s.details++
		return `{"result":{"posting_number":"label-http-posting","order_id":5,"order_number":"commercial-number-1","status":"awaiting_deliver","scheme":"FBS","delivery_method":{"warehouse_id":1020001323428000,"id":9007199254740993},"requirements":{"products_requiring_country":[],"products_requiring_change_country":[],"products_requiring_gtd":[],"products_requiring_mandatory_mark":[],"products_requiring_jw_uin":[],"products_requiring_rnpt":[],"products_requiring_imei":[],"products_requiring_weight":[]},"products":[{"sku":"2001","offer_id":"orders-offer","quantity":2,"price":"12.34","currency_code":"RUB","vat":"20"}]}}`, nil
	case "/v2/warehouse/list":
		s.discoveries++
		return `{"warehouses":[{"warehouse_id":1020001323428000,"warehouse_type":"fbs","status":"created","is_rfbs":false,"is_express":false}],"has_next":false,"cursor":""}`, nil
	case "/v2/delivery-method/list":
		s.discoveries++
		return `{"delivery_methods":[],"has_next":false,"cursor":""}`, nil
	case "/v3/posting/fbs/package-label/create":
		s.creates++
		if string(body) != `{"posting_numbers":["label-http-posting"]}` {
			return "", fmt.Errorf("label create contained unreviewed fields")
		}
		return `{"tasks":[{"task_id":99,"task_type":"big_label"}]}`, nil
	case "/v2/posting/fbs/package-label/get":
		s.polls++
		if string(body) != `{"task_id":99}` {
			return "", fmt.Errorf("label task identity changed")
		}
		if s.polls == 1 {
			return `{"status":{"code":"in_progress","postings_count":1,"printed_postings_count":0},"file_url":""}`, nil
		}
		return `{"status":{"code":"completed","postings_count":1,"printed_postings_count":1},"file_url":"https://api-seller.ozon.ru/labels/synthetic.pdf"}`, nil
	default:
		return "", fmt.Errorf("unplanned synthetic label endpoint")
	}
}

func labelHTTPResponse(request *http.Request, contentType, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Request: request, ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body))}
}

func (s *labelHTTPTransport) assertCounts(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creates != 1 || s.polls != 2 || s.pdfs != 1 || s.details != 2 || s.discoveries != 4 {
		t.Fatalf("network attempts create=%d poll=%d pdf=%d detail=%d discovery=%d", s.creates, s.polls, s.pdfs, s.details, s.discoveries)
	}
}
