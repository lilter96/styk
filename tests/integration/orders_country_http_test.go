//go:build integration

package integration

import (
	"bytes"
	"context"
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

type countryHTTPTransport struct {
	base                   *labelHTTPTransport
	mu                     sync.Mutex
	writes, lists          int
	resolved, loseResponse bool
}

func (s *countryHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Api-Key") != "synthetic-label-secret" || r.Header.Get("Client-Id") != s.base.account {
		return nil, fmt.Errorf("wrong country credential scope")
	}
	body := ""
	switch r.URL.Path {
	case "/v2/posting/fbs/product/country/list":
		s.lists++
		body = `{"result":[{"name":"Норвегия","country_iso_code":"NO"}]}`
	case "/v2/posting/fbs/product/country/set":
		s.writes++
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if string(raw) != `{"country_iso_code":"NO","posting_number":"label-http-posting","product_id":2001}` {
			return nil, fmt.Errorf("country declaration target or factual value changed")
		}
		s.resolved = true
		if s.loseResponse {
			return nil, io.ErrUnexpectedEOF
		}
		body = `{"product_id":2001,"is_gtd_needed":false}`
	case "/v2/delivery-method/list":
		body = `{"delivery_methods":[{"id":9007199254740993,"warehouse_id":1020001323428000,"is_express":false}],"has_next":false}`
	default:
		result, err := s.base.response(r.URL.Path, nil)
		if err != nil {
			return nil, err
		}
		body = strings.ReplaceAll(result, "awaiting_deliver", "awaiting_packaging")
		if !s.resolved {
			body = strings.ReplaceAll(body, `"products_requiring_country":[]`, `"products_requiring_country":["2001"]`)
		}
	}
	return labelHTTPResponse(r, "application/json", body), nil
}
func TestOrdersCountryHTTPNormalWorkerAcknowledgesRequirementOnly(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost_response_%t", lost), func(t *testing.T) { runCountryHTTP(t, lost) })
	}
}
func runCountryHTTP(t *testing.T, lost bool) {
	f := newLabelHTTPFixture(t)
	transport := &countryHTTPTransport{base: &labelHTTPTransport{account: f.config.Scope.OzonAccount}, loseResponse: lost}
	factory, labelProtector := labelHTTPFactory(t, f, transport.base)
	factory.NewOzon = func(account, secret string) (*ozon.Client, error) {
		return ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: account, APIKey: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, PageSize: 100, Attempts: 1, HTTPClient: &http.Client{Transport: transport}})
	}
	allow := func(s orders.Scope) bool { return s == f.config.Scope }
	protector := orderprivacy.ActionProtector{Vault: labelProtector.Vault, Store: f.db}
	service, err := orderflow.NewService(f.db, factory, orderflow.ServiceOptions{AllowScope: allow, ActionProtector: protector})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := orderflow.NewRunner(f.db, factory, orderflow.RunnerOptions{WorkerID: "country-http", AllowScope: allow, Service: service, ActionReader: protector, PollInterval: time.Millisecond})
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
	countryHTTPStoreCapability(t, f, binding.Client, posting)
	ordersStoreCommit(t, f, posting, "country-initial")
	countries, err := service.Countries(f.ctx, orders.CountriesRequest{Scope: f.config.Scope, PostingNumber: posting.PostingNumber})
	if err != nil || len(countries.RequiredSKUs) != 1 || len(countries.Countries) != 1 {
		t.Fatal("bounded country dictionary unavailable", err)
	}
	before := transport.lists
	foreign := f.config.Scope
	foreign.Tenant = "another-merchant"
	if _, err = service.Countries(f.ctx, orders.CountriesRequest{Scope: foreign, PostingNumber: posting.PostingNumber}); err == nil || transport.lists != before {
		t.Fatal("foreign tenant reached provider dictionary")
	}
	preview := exemplarHTTPPreview(t, f, service, orders.ActionCountrySet, orders.ActionParameters{CountrySKU: "2001", CountryISOCode: "NO", CountryConfirmed: true})
	op := labelHTTPApprove(t, f, preview)
	state := orders.StateAcknowledged
	if lost {
		state = orders.StateUnknown
	}
	exemplarHTTPRun(t, f, runner, op.ID, state)
	if !lost {
		state = orders.StateReconciled
	}
	exemplarHTTPRun(t, f, runner, op.ID, state)
	if lost {
		exemplarHTTPRun(t, f, runner, op.ID, orders.StateUnknown)
	}
	history, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	receipt := history.Operations[0].Targets[0].Receipt
	if receipt.CountryEvidence == nil || receipt.CountryEvidence.CountryValueObserved || receipt.CountryEvidence.Meaning != "country_requirement_resolution" || !receipt.CountryEvidence.RequirementResolved {
		t.Fatal("requirement readback was presented as ISO verification")
	}
	if !lost {
		countryHTTPRollbackGuard(t, f)
	}
	if transport.writes != 1 {
		t.Fatal("country write blindly replayed")
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work WHERE work::text LIKE '%"country_iso_code"%'`, 0)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_action_secrets`, 1)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM provider_call_permits WHERE is_write AND started_at IS NOT NULL`, 1)
}

func countryHTTPStoreCapability(t *testing.T, f ordersStoreFixture, client orders.Provider, posting orders.Posting) {
	t.Helper()
	reader, ok := client.(interface {
		ReadOrderDelivery(context.Context, orders.Scope, orders.Posting) (delivery.Profile, error)
	})
	if !ok {
		t.Fatal("delivery capability reader absent")
	}
	profile, err := reader.ReadOrderDelivery(f.ctx, f.config.Scope, posting)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.db.SaveDeliveryObservations(f.ctx, delivery.Scope{Tenant: f.config.Scope.Tenant, OzonAccount: f.config.Scope.OzonAccount}, []delivery.Profile{profile}); err != nil {
		t.Fatal(err)
	}
}

func countryHTTPRollbackGuard(t *testing.T, f ordersStoreFixture) {
	t.Helper()
	dsn := f.db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down-to", "58")
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", dsn, "down")
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("country declaration history prevents destructive rollback")) {
		t.Fatal("country intent history could be lost or wrong rollback failure")
	}
	catalogSyncMigrate(t, dsn, "up")
	if err = f.db.RequireCurrentSchema(f.ctx); err != nil {
		t.Fatal("failed rollback changed readiness", err)
	}
}
