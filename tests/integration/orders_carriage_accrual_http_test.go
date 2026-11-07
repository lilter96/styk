//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
)

type carriageReadHTTPFixture struct {
	positionHTTPFixture
	writes  atomic.Int32
	reads   atomic.Int32
	unknown atomic.Bool
}

func newCarriageReadHTTPFixture(t *testing.T) *carriageReadHTTPFixture {
	t.Helper()
	base := newPositionHTTPFixture(t)
	f := &carriageReadHTTPFixture{positionHTTPFixture: base}
	base.provider.base.server.Config.Handler = http.HandlerFunc(f.serve)
	f.merchant, f.session = commissionHTTPMerchant(t, base)
	f.ctx = identity.WithSession(base.ctx, f.session)
	body := f.scopeBody()
	body["config"], body["expected_revision"], body["idempotency_key"], body["read_enabled"] = f.config, f.config.Revision, "carriage-source-consent", true
	status, raw := f.call(t, http.MethodPost, "/api/orders/config", body, true)
	if status != 200 || json.Unmarshal(raw, &f.config) != nil {
		t.Fatal("source permission not saved", status, string(raw))
	}
	return f
}

func (f *carriageReadHTTPFixture) scopeBody() map[string]any {
	return map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount}
}

func (f *carriageReadHTTPFixture) query() string {
	return url.Values{"tenant": {f.config.Scope.Tenant}, "moysklad_account": {f.config.Scope.MoyskladAccount}, "ozon_account": {f.config.Scope.OzonAccount}}.Encode()
}

func (f *carriageReadHTTPFixture) call(t *testing.T, method, path string, body map[string]any, csrf bool) (int, []byte) {
	t.Helper()
	encoded := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		encoded = string(raw)
	}
	return f.merchant.request(t, method, path, encoded, csrf)
}

func (f *carriageReadHTTPFixture) serve(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/carriage/") || strings.Contains(r.URL.Path, "/finance/accrual/") {
		if r.Header.Get("Client-Id") != f.config.Scope.OzonAccount || r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+f.config.Scope.OzonAccount {
			w.WriteHeader(403)
			return
		}
	}
	switch r.URL.Path {
	case "/v1/carriage/create", "/v1/carriage/approve":
		f.writes.Add(1)
		w.WriteHeader(503)
	case "/v2/carriage/delivery/list":
		f.reads.Add(1)
		methods := []any{}
		if f.unknown.Load() {
			methods = append(methods, map[string]any{"future_method": true})
		}
		f.provider.base.reply(w, map[string]any{"methods": methods, "cursor": "", "has_next": false})
	case "/v1/finance/accrual/types":
		f.reads.Add(1)
		f.provider.base.reply(w, map[string]any{"accrual_types": []any{map[string]any{"id": 1, "name": "Synthetic service", "description": "Separate accrual source"}}})
	case "/v1/finance/accrual/by-day":
		f.reads.Add(1)
		f.provider.base.reply(w, map[string]any{"accruals": []any{}, "last_id": ""})
	case "/v1/finance/accrual/postings":
		f.reads.Add(1)
		f.provider.base.reply(w, map[string]any{"posting_accruals": []any{map[string]any{"posting_number": "position-http-posting", "accruals": []any{}}}})
	default:
		f.provider.serve(w, r)
	}
}

func TestOrdersCarriageHTTPEmptyOrUnverifiedMembershipNeverQueuesWrites(t *testing.T) {
	f := newCarriageReadHTTPFixture(t)
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "no batch", true: "unverified methods"}[unknown], func(t *testing.T) {
			f.unknown.Store(unknown)
			body := f.scopeBody()
			body["expected_revision"], body["action"], body["delivery_method_id"], body["all_blr_traceable"] = f.config.Revision, "CREATE", "4001", false
			status, raw := f.call(t, http.MethodPost, "/api/orders/carriage/preview", body, true)
			if unknown {
				if status != 503 {
					t.Fatal("schema drift must stop preview", status, string(raw))
				}
				return
			}
			var preview orders.CarriagePreview
			if status != 200 || json.Unmarshal(raw, &preview) != nil || len(preview.Validation) == 0 || preview.Plan.Snapshot.ObservedMethodCount != map[bool]int{false: 0, true: 1}[unknown] {
				t.Fatal("blocked full batch preview unavailable", status, string(raw))
			}
			body = f.scopeBody()
			body["preview_id"], body["idempotency_key"], body["acknowledge"], body["all_ready_consent"], body["physical_readiness_confirmed"] = preview.ID, "blocked-"+preview.ID, true, true, true
			status, _ = f.call(t, http.MethodPost, "/api/orders/carriage/approve", body, true)
			if status != 409 {
				t.Fatal("incomplete batch was approved", status)
			}
		})
	}
	runner, err := orderflow.NewCarriageRunner(f.db, f.factory, orderflow.CarriageRunnerOptions{WorkerID: "carriage-http-worker", AllowScope: func(scope orders.Scope) bool { return scope == f.config.Scope }})
	if err != nil {
		t.Fatal(err)
	}
	ran, err := runner.RunNext(f.ctx)
	if err != nil || ran || f.writes.Load() != 0 {
		t.Fatal("blocked batch reached writer", ran, err)
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_carriage_work`).Scan(&count); err != nil || count != 0 {
		t.Fatal("blocked batch leaked durable work", count, err)
	}
}

func TestOrdersAccrualHTTPSeparateDiagnosticsNeverEnrollNativeWork(t *testing.T) {
	f := newCarriageReadHTTPFixture(t)
	paths := []string{"/api/orders/finance/accrual/types?" + f.query(), "/api/orders/finance/accrual/by-day?" + f.query() + "&date=2026-09-01"}
	for _, path := range paths {
		status, raw := f.call(t, http.MethodGet, path, nil, false)
		var report orders.AccrualReport
		if status != 200 || json.Unmarshal(raw, &report) != nil || report.SourceFamily != "ACCRUAL" || !report.Complete || len(report.Fingerprint) != 64 {
			t.Fatal("separate real API diagnostic unavailable", status, string(raw))
		}
	}
	body := f.scopeBody()
	body["posting_numbers"] = []string{"position-http-posting"}
	if status, _ := f.call(t, http.MethodPost, "/api/orders/finance/accrual/postings", body, false); status != 403 {
		t.Fatal("CSRF bypassed", status)
	}
	status, raw := f.call(t, http.MethodPost, "/api/orders/finance/accrual/postings", body, true)
	var report orders.AccrualReport
	if status != 200 || json.Unmarshal(raw, &report) != nil || !report.Complete || report.Kind != orders.AccrualPostings {
		t.Fatal("scoped postings diagnostic failed", status, string(raw))
	}
	before := f.reads.Load()
	body = f.scopeBody()
	body["config"], body["expected_revision"], body["idempotency_key"], body["read_enabled"] = f.config, f.config.Revision, "accrual-revoke", false
	if status, _ = f.call(t, http.MethodPost, "/api/orders/config", body, true); status != 200 {
		t.Fatal("source revoke failed", status)
	}
	status, _ = f.call(t, http.MethodGet, paths[0], nil, false)
	if status != 409 || f.reads.Load() != before || f.writes.Load() != 0 {
		t.Fatal("revoked read reached provider", status, f.reads.Load())
	}
	var count int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM orders_finance_work)+(SELECT count(*) FROM orders_carriage_work)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("read-only diagnostics enrolled writes", count, err)
	}
	if len(f.provider.base.native) != 0 {
		t.Fatal("read-only diagnostic created native document")
	}
}
