//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
)

type exemplarHTTPTransport struct {
	base           *labelHTTPTransport
	mu             sync.Mutex
	sets, prepares int
	marked         bool
	loseSet        bool
	losePrepare    bool
}

func (s *exemplarHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Api-Key") != "synthetic-label-secret" || r.Header.Get("Client-Id") != s.base.account {
		return nil, fmt.Errorf("wrong exemplar credential")
	}
	var body string
	switch r.URL.Path {
	case "/v6/fbs/posting/product/exemplar/create-or-get":
		s.prepares++
		if s.losePrepare {
			return nil, io.ErrUnexpectedEOF
		}
		body = exemplarHTTPResponse(false, false)
	case "/v6/fbs/posting/product/exemplar/set":
		s.sets++
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(raw), `"exemplar_id":9007199254740993`) || !strings.Contains(string(raw), `"product_id":2001`) {
			return nil, fmt.Errorf("wrong exemplar identity")
		}
		s.marked = true
		if s.loseSet {
			return nil, io.ErrUnexpectedEOF
		}
		body = "{}"
	case "/v2/delivery-method/list":
		body = `{"delivery_methods":[{"id":9007199254740993,"warehouse_id":1020001323428000,"is_express":false}],"has_next":false}`
	case "/v5/fbs/posting/product/exemplar/status":
		body = exemplarHTTPResponse(s.marked, true)
	default:
		result, err := s.base.response(r.URL.Path, nil)
		if err != nil {
			return nil, err
		}
		body = strings.ReplaceAll(result, "awaiting_deliver", "awaiting_packaging")
		body = strings.ReplaceAll(body, `"products_requiring_mandatory_mark":[]`, `"products_requiring_mandatory_mark":[2001]`)
	}
	return labelHTTPResponse(r, "application/json", body), nil
}
func exemplarHTTPResponse(marked, status bool) string {
	state := ""
	if status {
		state = `,"status":"update_available"`
		if marked {
			state = `,"status":"ship_available"`
		}
	}
	units := make([]map[string]any, 0, 2)
	for i, id := range []int64{9007199254740993, 9007199254740994} {
		marks := []map[string]string{}
		if marked {
			marks = append(marks, map[string]string{"mark_type": "mandatory_mark", "mark": fmt.Sprintf("SYNTHETIC-MARK-%d", i), "check_status": "passed"})
		}
		units = append(units, map[string]any{"exemplar_id": id, "is_gtd_absent": true, "is_rnpt_absent": true, "marks": marks})
	}
	raw, err := json.Marshal(units)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(`{"posting_number":"label-http-posting"%s,"products":[{"product_id":2001,"quantity":2,"is_mandatory_mark_needed":true,"exemplars":%s}]}`, state, raw)
}

func TestOrdersExemplarHTTPSealedPreparationAndSetReconcile(t *testing.T) {
	for _, mode := range []string{"acknowledged", "lost_set_response", "lost_prepare_response"} {
		t.Run(mode, func(t *testing.T) { runExemplarHTTP(t, mode) })
	}
}

func runExemplarHTTP(t *testing.T, mode string) {
	f := newLabelHTTPFixture(t)
	transport := &exemplarHTTPTransport{base: &labelHTTPTransport{account: f.config.Scope.OzonAccount}, loseSet: mode == "lost_set_response", losePrepare: mode == "lost_prepare_response"}
	factory, labelProtector := labelHTTPFactory(t, f, transport.base)
	factory.NewOzon = func(account, secret string) (*ozon.Client, error) {
		return ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: account, APIKey: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, PageSize: 100, Attempts: 1, HTTPClient: &http.Client{Transport: transport}})
	}
	protector := orderprivacy.ExemplarProtector{Vault: labelProtector.Vault, Store: f.db}
	actionProtector := orderprivacy.ActionProtector{Vault: labelProtector.Vault, Store: f.db}
	allow := func(s orders.Scope) bool { return s == f.config.Scope }
	service, err := orderflow.NewService(f.db, factory, orderflow.ServiceOptions{AllowScope: allow, ExemplarReader: protector, ActionProtector: actionProtector})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := orderflow.NewRunner(f.db, factory, orderflow.RunnerOptions{WorkerID: "exemplar-http", AllowScope: allow, Service: service, ExemplarReader: protector, ExemplarSealer: protector, ActionReader: actionProtector, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 2)
	if err != nil {
		t.Fatal(err)
	}
	posting, err := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, "label-http-posting")
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := binding.Client.(interface {
		ReadOrderDelivery(context.Context, orders.Scope, orders.Posting) (delivery.Profile, error)
	})
	if !ok {
		t.Fatal("delivery reader missing")
	}
	profile, err := reader.ReadOrderDelivery(f.ctx, f.config.Scope, posting)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.db.SaveDeliveryObservations(f.ctx, delivery.Scope{Tenant: f.config.Scope.Tenant, OzonAccount: f.config.Scope.OzonAccount}, []delivery.Profile{profile}); err != nil {
		t.Fatal(err)
	}
	ordersStoreCommit(t, f, posting, "exemplar-initial")
	preview := exemplarHTTPPreview(t, f, service, orders.ActionExemplarPrepare, orders.ActionParameters{})
	op := ordersStoreApprove(t, f, preview)
	first := orders.StateAcknowledged
	if transport.losePrepare {
		first = orders.StateUnknown
	}
	exemplarHTTPRun(t, f, runner, op.ID, first)
	exemplarHTTPRun(t, f, runner, op.ID, orders.StateReconciled)
	summary, err := f.db.OrderExemplarPublic(f.ctx, f.config.Scope, posting.PostingNumber)
	if err != nil || summary.WorkID == "" {
		t.Fatal("prepared exact IDs unavailable", err)
	}
	noop, noopErr := service.Preview(f.ctx, orders.PreviewRequest{Scope: f.config.Scope, ExpectedRevision: f.config.Revision, PostingNumbers: []string{posting.PostingNumber}, Actor: f.session.AuditID, Actions: []orders.Action{orders.ActionExemplarSet}, Proposals: []orders.ActionProposal{{PostingNumber: posting.PostingNumber, Action: orders.ActionExemplarSet, Parameters: orders.ActionParameters{ExemplarWorkID: summary.WorkID, Exemplars: &orders.ExemplarSetInput{}}}}})
	if noopErr != nil || len(noop.Decisions) != 1 || noop.Decisions[0].Ready {
		t.Fatal("no-op exemplar set became a write", noopErr)
	}
	input := &orders.ExemplarSetInput{Products: []orders.ExemplarProductInput{{SKU: "2001", Exemplars: []orders.ExemplarInput{{ID: "9007199254740993", Marks: orders.Present([]orders.ExemplarMark{{Type: "mandatory_mark", Value: "SYNTHETIC-MARK-0"}})}, {ID: "9007199254740994", Marks: orders.Present([]orders.ExemplarMark{{Type: "mandatory_mark", Value: "SYNTHETIC-MARK-1"}})}}}}}
	preview = exemplarHTTPPreview(t, f, service, orders.ActionExemplarSet, orders.ActionParameters{ExemplarWorkID: summary.WorkID, Exemplars: input})
	op = ordersStoreApprove(t, f, preview)
	first = orders.StatePolling
	if transport.loseSet {
		first = orders.StateUnknown
	}
	exemplarHTTPRun(t, f, runner, op.ID, first)
	exemplarHTTPRun(t, f, runner, op.ID, orders.StateReconciled)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_exemplar_secrets`, 2)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work WHERE action IN ('posting_exemplar_prepare','posting_exemplar_set') AND state='RECONCILED' AND dispatch_count=1`, 2)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work WHERE work::text LIKE '%SYNTHETIC-MARK%'`, 0)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_receipts WHERE receipt::text LIKE '%SYNTHETIC-MARK%'`, 0)
	if mode != "acknowledged" {
		assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_receipts WHERE receipt->>'state'='UNKNOWN'`, 1)
	}
	if transport.prepares != 1 || transport.sets != 1 {
		t.Fatal("exemplar write replayed")
	}
	review, err := f.db.OrderReviewContext(f.ctx, f.config.Scope)
	if err != nil || !review.ExemplarProofs[posting.PostingNumber].Verified {
		t.Fatal("reconciled packing proof unavailable", err)
	}
	if mode == "acknowledged" {
		proof := review.ExemplarProofs[posting.PostingNumber]
		exemplarHTTPPreview(t, f, service, orders.ActionPostingPack, orders.ActionParameters{ExemplarWorkID: proof.SnapshotID, ReadinessConfirmed: true, Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{{Identity: posting.Lines[0].Identity, Quantity: "2"}}}}})
		assertExemplarRollbackRejected(t, f)
	}
	foreign := f.config.Scope
	foreign.Tenant = "different"
	if _, err = f.db.OrderExemplarPublic(f.ctx, foreign, posting.PostingNumber); err == nil {
		t.Fatal("cross-tenant summary exposed")
	}
}
func exemplarHTTPPreview(t *testing.T, f ordersStoreFixture, s *orderflow.Service, action orders.Action, p orders.ActionParameters) orders.Preview {
	t.Helper()
	result, err := s.Preview(f.ctx, orders.PreviewRequest{Scope: f.config.Scope, ExpectedRevision: f.config.Revision, PostingNumbers: []string{"label-http-posting"}, Actor: f.session.AuditID, Actions: []orders.Action{action}, Proposals: []orders.ActionProposal{{PostingNumber: "label-http-posting", Action: action, Parameters: p}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Decisions) != 1 || !result.Decisions[0].Ready {
		t.Fatalf("exemplar preview blocked: %+v", result.Decisions)
	}
	return result
}
func exemplarHTTPRun(t *testing.T, f ordersStoreFixture, r *orderflow.Runner, id string, want orders.State) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	for {
		ran, err := r.RunNext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ran {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("exemplar phase not claimed")
		case <-time.After(time.Millisecond):
		}
	}
	h, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range h.Operations {
		if op.ID == id {
			if op.State != want {
				t.Fatalf("expected %s got %s %+v", want, op.State, op.Targets)
			}
			return
		}
	}
	t.Fatal("operation missing")
}

func assertExemplarRollbackRejected(t *testing.T, f ordersStoreFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", f.db.Pool.Config().ConnString(), "down")
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("exemplar intent history prevents destructive rollback")) {
		t.Fatal("exemplar migration allowed history loss or failed for unrelated reason")
	}
	if err = f.db.RequireCurrentSchema(f.ctx); err != nil {
		t.Fatal("failed rollback changed schema", err)
	}
}
