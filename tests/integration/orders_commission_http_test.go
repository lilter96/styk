//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

type commissionHTTPProvider struct {
	base     *positionHTTPProvider
	scope    orders.Scope
	config   orders.Config
	period   orders.RealizationPeriod
	mu       sync.Mutex
	document map[string]any
	writes   atomic.Int32
	lost     atomic.Bool
	changed  atomic.Bool
}

type commissionHTTPFixture struct {
	positionHTTPFixture
	finance       *commissionHTTPProvider
	financeRunner *orderflow.CommissionRunner
}

func newCommissionHTTPFixture(t *testing.T) *commissionHTTPFixture {
	t.Helper()
	base := newPositionHTTPFixture(t)
	now := time.Now().UTC()
	previous := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	provider := &commissionHTTPProvider{base: base.provider, scope: base.config.Scope, config: base.config, period: orders.RealizationPeriod{Year: previous.Year(), Month: int(previous.Month())}}
	base.provider.base.server.Config.Handler = http.HandlerFunc(provider.serve)
	f := &commissionHTTPFixture{positionHTTPFixture: base, finance: provider}
	f.merchant, f.session = commissionHTTPMerchant(t, base)
	f.ctx = identity.WithSession(base.ctx, f.session)
	var metadata orders.CommissionMetadata
	query := url.Values{"tenant": {f.config.Scope.Tenant}, "moysklad_account": {f.config.Scope.MoyskladAccount}, "ozon_account": {f.config.Scope.OzonAccount}}
	if err := json.Unmarshal(f.request(t, http.MethodGet, "/api/orders/finance/metadata?"+query.Encode(), nil), &metadata); err != nil || len(metadata.Contracts) != 1 {
		t.Fatal("finance metadata must be readable before policy enrollment", err)
	}
	policy := &orders.CommissionPolicy{Enabled: true, OrganizationID: base.config.OrganizationID, AgentID: base.config.AgentID, ContractID: ordersProcessID(40001), CurrencyID: base.config.CurrencyID, CurrencyISO: "RUB", RateValue: "1", PriceBasis: orders.CommissionSellerPrice, VATEnabled: orders.Present(false), VATIncluded: orders.Present(false), VAT: "0", OtherComponents: orders.CommissionZeroOtherComponents}
	config := base.config
	config.Finance = policy
	body := f.scopeBody()
	body["config"], body["expected_revision"], body["idempotency_key"], body["finance_consent"], body["read_enabled"] = config, config.Revision, "finance-http-consent", true, true
	raw := f.request(t, http.MethodPost, "/api/orders/config", body)
	if err := json.Unmarshal(raw, &f.config); err != nil || f.config.Finance == nil || !f.config.Finance.Enabled {
		t.Fatal("finance owner policy not saved", err)
	}
	f.restartFinance(t)
	return f
}

func commissionHTTPMerchant(t *testing.T, f positionHTTPFixture) (*merchantBrowser, identity.Session) {
	t.Helper()
	mailbox := &testMailbox{tokens: map[string]string{}}
	handler, err := console.MerchantLoopbackTestHandler(f.db, "http://127.0.0.1:5173", mailbox, f.provider.base.server.URL, f.provider.base.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store, err := identity.New(f.db.Pool, f.db.Timeout)
	if err != nil {
		t.Fatal(err)
	}
	session, token, err := store.CreateSession(f.ctx, f.session.User)
	if err != nil {
		t.Fatal(err)
	}
	merchant := newMerchantBrowser(t, server.URL)
	address, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	merchant.client.Jar.SetCookies(address, []*http.Cookie{{Name: "styk_session", Value: token, Path: "/", HttpOnly: true}})
	merchant.csrf = session.CSRF
	return merchant, session
}

func (f *commissionHTTPFixture) scopeBody() map[string]any {
	return map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount}
}

func (f *commissionHTTPFixture) request(t *testing.T, method, path string, body map[string]any) []byte {
	t.Helper()
	encoded := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		encoded = string(raw)
	}
	status, raw := f.merchant.request(t, method, path, encoded, method != http.MethodGet)
	if status != http.StatusOK {
		t.Fatalf("finance merchant request %s %s: HTTP %d: %s", method, path, status, raw)
	}
	return raw
}

func (f *commissionHTTPFixture) approveFinance(t *testing.T) orders.CommissionWork {
	t.Helper()
	body := f.scopeBody()
	body["expected_revision"], body["period"] = f.config.Revision, f.finance.period
	raw := f.request(t, http.MethodPost, "/api/orders/finance/preview", body)
	var preview orders.CommissionPreview
	if err := json.Unmarshal(raw, &preview); err != nil || len(preview.Validation) != 0 || preview.Plan.Validate() != nil {
		t.Fatalf("finance preview blocked: %s, decode=%v", raw, err)
	}
	body = f.scopeBody()
	body["preview_id"], body["idempotency_key"], body["acknowledge"] = preview.ID, "finance-http-approve", true
	raw = f.request(t, http.MethodPost, "/api/orders/finance/approve", body)
	var work orders.CommissionWork
	if err := json.Unmarshal(raw, &work); err != nil || work.ID == "" || work.State != orders.StateQueued {
		t.Fatal("finance durable approval absent", err, string(raw))
	}
	var repeated orders.CommissionWork
	if err := json.Unmarshal(f.request(t, http.MethodPost, "/api/orders/finance/approve", body), &repeated); err != nil || repeated.ID != work.ID {
		t.Fatal("repeated finance approval duplicated work", err)
	}
	return work
}

func (f *commissionHTTPFixture) restartFinance(t *testing.T) {
	t.Helper()
	allow := func(scope orders.Scope) bool { return scope == f.config.Scope }
	var err error
	f.service, err = orderflow.NewService(f.db, f.factory, orderflow.ServiceOptions{AllowScope: allow})
	if err != nil {
		t.Fatal(err)
	}
	f.financeRunner, err = orderflow.NewCommissionRunner(f.db, f.factory, orderflow.CommissionRunnerOptions{WorkerID: "finance-http-worker", AllowScope: allow, Service: f.service, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *commissionHTTPFixture) latestFinance(t *testing.T) orders.CommissionWork {
	t.Helper()
	items, err := f.db.CommissionHistory(f.ctx, f.config.Scope, 10)
	if err != nil || len(items) != 1 {
		t.Fatal("finance journal unavailable", len(items), err)
	}
	return items[0]
}

func (f *commissionHTTPFixture) runFinance(t *testing.T, want orders.State) orders.CommissionWork {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	for {
		worked, err := f.financeRunner.RunNext(ctx)
		if err != nil {
			t.Fatal("finance normal worker phase", err)
		}
		if worked {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	work := f.latestFinance(t)
	if work.State != want {
		t.Fatalf("finance state=%s code=%s, want %s", work.State, work.Code, want)
	}
	return work
}

func (p *commissionHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v2/finance/realization" {
		if r.Header.Get("Client-Id") != p.scope.OzonAccount || r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+p.scope.OzonAccount {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		p.report(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/entity/commissionreportin") || strings.HasPrefix(r.URL.Path, "/entity/contract") || r.URL.Path == "/entity/organization/"+p.config.OrganizationID || r.URL.Path == "/entity/counterparty/"+p.config.AgentID || r.URL.Path == "/entity/currency/"+p.config.CurrencyID || r.URL.Path == "/entity/product/"+ordersProcessID(8) {
		if r.Header.Get("Authorization") != "Bearer synthetic-orders-moysklad-"+p.scope.MoyskladAccount {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		p.native(w, r)
		return
	}
	p.base.serve(w, r)
}

func (p *commissionHTTPProvider) report(w http.ResponseWriter, r *http.Request) {
	var period orders.RealizationPeriod
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&period) != nil || period != p.period {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	start := time.Date(period.Year, time.Month(period.Month), 1, 0, 0, 0, 0, time.UTC)
	price, amount, total := json.Number("100.00"), json.Number("200.00"), json.Number("185.00")
	if p.changed.Load() {
		price, amount, total = json.Number("101.00"), json.Number("202.00"), json.Number("187.00")
	}
	zero := map[string]any{"quantity": 0, "amount": 0, "bonus": 0, "standard_fee": 0, "bank_coinvestment": 0, "pick_up_point_coinvestment": 0, "stars": 0, "total": 0, "price_per_instance": 0}
	sales := map[string]any{"quantity": 2, "amount": amount, "bonus": 5, "standard_fee": 20, "bank_coinvestment": 0, "pick_up_point_coinvestment": 0, "stars": 0, "total": total, "price_per_instance": price}
	header := map[string]any{"number": "finance-http-realization", "currency_sys_name": "RUB", "doc_date": start.AddDate(0, 1, 4).Format("2006-01-02"), "start_date": start.Format("2006-01-02"), "stop_date": start.AddDate(0, 1, -1).Format("2006-01-02")}
	p.base.base.reply(w, map[string]any{"result": map[string]any{"header": header, "rows": []any{map[string]any{"rowNumber": 1, "item": map[string]any{"sku": json.Number("2001"), "offer_id": "process-offer"}, "seller_price_per_instance": price, "delivery_commission": sales, "return_commission": zero}}}})
}

func (p *commissionHTTPProvider) reference(kind, id string) map[string]any {
	return map[string]any{"meta": map[string]any{"href": p.base.base.server.URL + "/entity/" + kind + "/" + id, "type": kind, "mediaType": "application/json"}}
}

func (p *commissionHTTPProvider) entity(kind, id string) map[string]any {
	result := p.reference(kind, id)
	result["id"], result["accountId"], result["archived"] = id, p.scope.MoyskladAccount, false
	if kind == "organization" || kind == "counterparty" {
		result["accounts"] = map[string]any{"meta": map[string]any{"href": p.base.base.server.URL + "/entity/" + kind + "/" + id + "/accounts", "size": 0}}
	}
	if kind == "currency" {
		result["isoCode"] = "RUB"
	}
	if kind == "contract" {
		result["name"], result["contractType"], result["rewardType"] = "Commission HTTP contract", "Commission", "None"
		result["ownAgent"], result["agent"], result["rate"] = p.reference("organization", p.config.OrganizationID), p.reference("counterparty", p.config.AgentID), map[string]any{"currency": p.reference("currency", p.config.CurrencyID)}
	}
	return result
}

func (p *commissionHTTPProvider) native(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/entity/commissionreportin" {
		p.commission(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/entity/commissionreportin/") {
		p.readCommission(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/entity/"), "/")
	if len(parts) == 1 && parts[0] == "contract" {
		p.collection(w, 100, []any{p.entity("contract", ordersProcessID(40001))})
		return
	}
	if len(parts) != 2 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	p.base.base.reply(w, p.entity(parts[0], parts[1]))
}

func (p *commissionHTTPProvider) collection(w http.ResponseWriter, limit int, rows []any) {
	p.base.base.reply(w, map[string]any{"meta": map[string]any{"size": len(rows), "limit": limit, "offset": 0}, "rows": rows})
}

func (p *commissionHTTPProvider) commission(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Method == http.MethodGet {
		rows := make([]any, 0, 1)
		if p.document != nil && r.URL.Query().Get("filter") == "externalCode="+p.document["externalCode"].(string) {
			rows = append(rows, p.header())
		}
		p.collection(w, 2, rows)
		return
	}
	if r.Method != http.MethodPost || p.document != nil {
		w.WriteHeader(http.StatusConflict)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.UseNumber()
	var body map[string]any
	if decoder.Decode(&body) != nil || body["applicable"] != false || body["rewardType"] != "None" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	p.document = body
	p.writes.Add(1)
	if p.lost.Load() {
		p.base.dropResponse(w)
		return
	}
	p.base.base.reply(w, p.header())
}

// Caller holds mu. The persisted request remains immutable; collection metadata
// belongs only to readback responses, matching the actual MS entity contract.
func (p *commissionHTTPProvider) header() map[string]any {
	result := make(map[string]any, len(p.document)+10)
	for key, value := range p.document {
		result[key] = value
	}
	id := ordersProcessID(40002)
	result["id"], result["accountId"], result["meta"], result["updated"] = id, p.scope.MoyskladAccount, p.reference("commissionreportin", id)["meta"], "2026-11-06 12:00:00.000"
	result["positions"] = map[string]any{"meta": map[string]any{"href": p.base.base.server.URL + "/entity/commissionreportin/" + id + "/positions", "size": len(p.document["positions"].([]any))}}
	result["returnToCommissionerPositions"] = map[string]any{"meta": map[string]any{"href": p.base.base.server.URL + "/entity/commissionreportin/" + id + "/returntocommissionerpositions", "size": 0}}
	result["sum"], result["commitentSum"] = json.Number("20000"), json.Number("18500")
	return result
}

func (p *commissionHTTPProvider) readCommission(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	path := "/entity/commissionreportin/" + ordersProcessID(40002)
	if p.document == nil || r.Method != http.MethodGet {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.URL.Path == path {
		p.base.base.reply(w, p.header())
		return
	}
	if r.URL.Path != path+"/positions" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	rows := make([]any, 0, len(p.document["positions"].([]any)))
	for index, value := range p.document["positions"].([]any) {
		row := make(map[string]any, len(value.(map[string]any))+2)
		for key, field := range value.(map[string]any) {
			row[key] = field
		}
		row["id"], row["accountId"] = ordersProcessID(40100+index), p.scope.MoyskladAccount
		rows = append(rows, row)
	}
	p.collection(w, 100, rows)
}

func TestOrdersCommissionHTTPPreviewApprovalOutboxAndExactReadback(t *testing.T) {
	f := newCommissionHTTPFixture(t)
	f.approveFinance(t)
	f.runFinance(t, orders.StateAcknowledged)
	work := f.runFinance(t, orders.StateReconciled)
	if f.finance.writes.Load() != 1 || work.DispatchCount != 1 || work.Plan.Totals.Gross != "200" || work.Plan.Totals.Reward != "15" || work.Plan.Totals.Net != "185" {
		t.Fatal("authoritative monthly report was not one exact draft", work)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM outbox_tasks WHERE task_type='orders_finance'`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work`, 0)
	if worked, err := f.financeRunner.RunNext(f.ctx); err != nil || worked || f.finance.writes.Load() != 1 {
		t.Fatal("completed finance work replayed", worked, err)
	}
	query := url.Values{"tenant": {f.config.Scope.Tenant}, "moysklad_account": {f.config.Scope.MoyskladAccount}, "ozon_account": {f.config.Scope.OzonAccount}}
	var history struct {
		Operations []orders.CommissionWork `json:"operations"`
	}
	if err := json.Unmarshal(f.request(t, http.MethodGet, "/api/orders/finance/operations?"+query.Encode(), nil), &history); err != nil || len(history.Operations) != 1 || history.Operations[0].State != orders.StateReconciled {
		t.Fatal("merchant finance journal missing", err)
	}
}

func TestOrdersCommissionHTTPLostAcknowledgementNeverReposts(t *testing.T) {
	f := newCommissionHTTPFixture(t)
	f.approveFinance(t)
	f.finance.lost.Store(true)
	f.runFinance(t, orders.StateUnknown)
	f.restartFinance(t)
	work := f.runFinance(t, orders.StateReconciled)
	if f.finance.writes.Load() != 1 || work.DispatchCount != 1 || work.Receipt.State != orders.StateUnknown {
		t.Fatal("UNKNOWN replay fabricated acknowledgement or duplicated POST", work)
	}
}

func TestOrdersCommissionHTTPSourceCorrectionBlocksUnsentDraft(t *testing.T) {
	f := newCommissionHTTPFixture(t)
	f.approveFinance(t)
	f.finance.changed.Store(true)
	work := f.runFinance(t, orders.StateBlocked)
	if f.finance.writes.Load() != 0 || work.DispatchCount != 0 || !work.SourceChanged {
		t.Fatal("changed source posted a stale report", work)
	}
}

type commissionAdmissionStore struct {
	providercall.Store
	once  atomic.Bool
	after func() error
}

func (s *commissionAdmissionStore) StartProviderCall(ctx context.Context, lease providercall.Lease) (time.Duration, error) {
	delay, err := s.Store.StartProviderCall(ctx, lease)
	if err == nil && delay == 0 && lease.Attempt.IsWrite && lease.Attempt.Scope.Provider == "moysklad" && s.once.CompareAndSwap(false, true) {
		err = s.after()
	}
	return delay, err
}

func TestOrdersCommissionHTTPFinalAdmissionRechecksOwnerConsent(t *testing.T) {
	f := newCommissionHTTPFixture(t)
	f.approveFinance(t)
	hook := &commissionAdmissionStore{Store: f.db, after: func() error {
		config := f.config
		policy := *config.Finance
		policy.Enabled = false
		config.Finance = &policy
		_, err := f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "finance-admission-revoke"}, time.Now())
		return err
	}}
	gate, err := providercall.New(hook, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	f.factory.Gate = gate
	f.restartFinance(t)
	work := f.runFinance(t, orders.StateBlocked)
	if !hook.once.Load() || f.finance.writes.Load() != 0 || work.DispatchCount != 0 {
		t.Fatal("owner revocation after provider admission reached POST", fmt.Sprint(work.State), work.Code)
	}
}
