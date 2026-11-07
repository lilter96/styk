package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

type commissionSimulator struct {
	server   *httptest.Server
	adapter  *OrderClient
	plan     orders.CommissionPlan
	created  atomic.Bool
	writes   atomic.Int32
	mode     string
	captured chan map[string]any
}

func newCommissionSimulator(t *testing.T, mode string) *commissionSimulator {
	t.Helper()
	sim := &commissionSimulator{mode: mode, captured: make(chan map[string]any, 2)}
	sim.server = httptest.NewServer(http.HandlerFunc(sim.serve))
	t.Cleanup(sim.server.Close)
	client, err := New(Options{BaseURL: sim.server.URL + "/api/remap/1.2/", Token: "synthetic", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 3, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	scope := orderTestCommand(t).Scope
	sim.adapter, err = NewOrderClient(client, scope)
	if err != nil {
		t.Fatal(err)
	}
	sim.plan = commissionAdapterPlan(t, sim.adapter, scope)
	return sim
}

func commissionAdapterPlan(t *testing.T, adapter *OrderClient, scope orders.Scope) orders.CommissionPlan {
	t.Helper()
	now := time.Now().UTC()
	policy := &orders.CommissionPolicy{ID: "policy", Scope: scope, Revision: 1, ConfigRevision: 1, OwnerID: "owner", Enabled: true, GrantedAt: now.Add(-time.Minute), OrganizationID: orderTestID(2), AgentID: orderTestID(3), ContractID: orderTestID(20), CurrencyID: orderTestID(7), CurrencyISO: "RUB", RateValue: "1", PriceBasis: orders.CommissionSellerPrice, VATEnabled: orders.Present(false), VATIncluded: orders.Present(false), VAT: "0", OtherComponents: orders.CommissionZeroOtherComponents}
	refs, err := adapter.ReadCommissionReferences(context.Background(), scope, *policy)
	if err != nil {
		t.Fatal(err)
	}
	now = time.Now().UTC()
	zero := orders.Present(orders.Decimal("0"))
	returns := orders.RealizationAmounts{Presence: orders.PresenceValue, Quantity: zero, Amount: zero, Bonus: zero, StandardFee: zero, BankCoinvestment: zero, PickUpPointCoinvestment: zero, Stars: zero, Total: zero}
	delivery := returns
	delivery.Quantity, delivery.Amount, delivery.Bonus, delivery.StandardFee, delivery.Total, delivery.PricePerInstance = orders.Present(orders.Decimal("2")), orders.Present(orders.Decimal("200")), orders.Present(orders.Decimal("25")), orders.Present(orders.Decimal("20")), orders.Present(orders.Decimal("205")), orders.Present(orders.Decimal("100"))
	period := now.AddDate(0, -1, 0)
	start := time.Date(period.Year(), period.Month(), 1, 0, 0, 0, 0, time.UTC)
	source := orders.RealizationReport{Scope: scope, Period: orders.RealizationPeriod{Year: start.Year(), Month: int(start.Month())}, Number: "REPORT-1", Currency: "RUB", DocumentDate: orders.Present(now.Format("2006-01-02")), StartDate: orders.Present(start.Format("2006-01-02")), StopDate: orders.Present(start.AddDate(0, 1, -1).Format("2006-01-02")), Rows: []orders.RealizationRow{{Number: 1, SKU: orders.Present("sku"), OfferID: orders.Present("offer"), SellerUnitPrice: orders.Present(orders.Decimal("100")), Delivery: delivery, Return: returns}}, ObservedAt: now}
	source.Fingerprint, err = ordermoney.CommissionSourceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	mapping := orders.ProductMappingRef{ID: "mapping", Version: 1, Scope: scope, OzonProductID: "product", SKU: "sku", OfferID: "offer", MoyskladKind: "product", MoyskladID: orderTestID(8), Confirmed: true, ConfirmedAt: now}
	plan, issues := ordermoney.PlanCommissionDraft(orders.Config{Scope: scope, Revision: 1, Finance: policy}, orders.CredentialRevisions{Ozon: 1, Moysklad: 1}, source, []orders.ProductMappingRef{mapping}, refs, "preview", now)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	return plan
}

func (s *commissionSimulator) ref(path, kind string, id int) map[string]any {
	return map[string]any{"meta": map[string]any{"href": s.server.URL + "/api/remap/1.2/" + path + "/" + orderTestID(id), "type": kind, "mediaType": "application/json"}}
}

func (s *commissionSimulator) entity(kind string, id int) map[string]any {
	row := s.ref("entity/"+kind, kind, id)
	row["id"], row["accountId"], row["archived"] = orderTestID(id), orderTestID(1), false
	return row
}

func (s *commissionSimulator) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/api/remap/1.2/")
	if r.Method == http.MethodPost {
		s.write(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	if path == "context/employee" {
		s.reply(w, map[string]any{"accountId": orderTestID(1)})
		return
	}
	if path == "entity/commissionreportin" {
		s.lookup(w)
		return
	}
	if path == "entity/commissionreportin/"+orderTestID(30) {
		s.reply(w, s.document())
		return
	}
	if path == "entity/commissionreportin/"+orderTestID(30)+"/positions" {
		s.positionPage(w, r)
		return
	}
	s.references(w, path)
}

func (s *commissionSimulator) references(w http.ResponseWriter, path string) {
	switch path {
	case "entity/organization/" + orderTestID(2), "entity/counterparty/" + orderTestID(3):
		kind, id := "organization", 2
		if strings.Contains(path, "counterparty") {
			kind, id = "counterparty", 3
		}
		row := s.entity(kind, id)
		row["accounts"] = map[string]any{"meta": map[string]any{"href": s.server.URL + "/api/remap/1.2/" + path + "/accounts", "size": 0}}
		s.reply(w, row)
	case "entity/currency/" + orderTestID(7):
		row := s.entity("currency", 7)
		row["isoCode"] = "RUB"
		s.reply(w, row)
	case "entity/contract/" + orderTestID(20), "entity/contract":
		row := s.entity("contract", 20)
		row["name"], row["contractType"], row["rewardType"] = "Commission contract", "Commission", "None"
		row["ownAgent"], row["agent"], row["rate"] = s.ref("entity/organization", "organization", 2), s.ref("entity/counterparty", "counterparty", 3), map[string]any{"currency": s.ref("entity/currency", "currency", 7)}
		if s.mode == "wrong contract" && s.plan.ID != "" {
			row["agent"] = s.ref("entity/counterparty", "counterparty", 99)
		}
		if path == "entity/contract" {
			s.page(w, []any{row}, 1, 100, 0)
			return
		}
		s.reply(w, row)
	case "entity/product/" + orderTestID(8):
		s.reply(w, s.entity("product", 8))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *commissionSimulator) identity() map[string]any {
	row := s.entity("commissionreportin", 30)
	row["externalCode"] = s.plan.ExternalCode
	return row
}

func (s *commissionSimulator) document() map[string]any {
	row, err := s.adapter.commissionPayload(s.plan)
	if err != nil {
		return nil
	}
	for key, value := range s.identity() {
		row[key] = value
	}
	row["updated"], row["sum"], row["commitentSum"] = "2026-11-07 12:00:00.000", 20000, 20500
	row["positions"] = map[string]any{"meta": map[string]any{"href": s.server.URL + "/api/remap/1.2/entity/commissionreportin/" + orderTestID(30) + "/positions", "size": 1}}
	row["returnToCommissionerPositions"] = map[string]any{"meta": map[string]any{"href": s.server.URL + "/api/remap/1.2/entity/commissionreportin/" + orderTestID(30) + "/returntocommissionerpositions", "size": 0}}
	if s.mode == "applied" {
		row["applicable"] = true
	}
	if s.mode == "wrong reward" {
		row["commitentSum"] = 20400
	}
	return row
}

func (s *commissionSimulator) positionPage(w http.ResponseWriter, r *http.Request) {
	position := map[string]any{"id": orderTestID(31), "accountId": orderTestID(1), "assortment": s.ref("entity/product", "product", 8), "quantity": 2, "price": 10000, "reward": -500, "vat": 0, "vatEnabled": false}
	if s.mode == "wrong reward" {
		position["reward"] = -400
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		w.WriteHeader(400)
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil {
		w.WriteHeader(400)
		return
	}
	s.page(w, []any{position}, 1, limit, offset)
}

func (s *commissionSimulator) lookup(w http.ResponseWriter) {
	rows := []any{}
	if s.created.Load() {
		rows = append(rows, s.identity())
	}
	if s.mode == "duplicates" {
		rows = []any{s.identity(), s.identity()}
	}
	s.page(w, rows, len(rows), 2, 0)
}

func (s *commissionSimulator) write(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/remap/1.2/entity/commissionreportin" {
		w.WriteHeader(405)
		return
	}
	s.writes.Add(1)
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	s.captured <- body
	if s.mode == "rejected" {
		w.WriteHeader(422)
		s.reply(w, map[string]any{"errors": []any{}})
		return
	}
	s.created.Store(true)
	if s.mode == "lost" {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			if closeErr := connection.Close(); closeErr != nil {
				return
			}
		}
		return
	}
	s.reply(w, s.identity())
}

func (s *commissionSimulator) reply(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return
	}
}
func (s *commissionSimulator) page(w http.ResponseWriter, rows []any, size, limit, offset int) {
	s.reply(w, map[string]any{"meta": map[string]any{"size": size, "limit": limit, "offset": offset}, "rows": rows})
}

func TestCommissionAdapterDraftWriteReadbackAndLostAck(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"normal", "lost"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			sim := newCommissionSimulator(t, mode)
			receipt, err := sim.adapter.WriteCommissionDraft(context.Background(), sim.plan, func(context.Context) (bool, error) { return true, nil })
			if mode == "normal" && (err != nil || receipt.State != orders.StateAcknowledged) || mode == "lost" && (!errors.Is(err, ErrOrderUnknown) || receipt.State != orders.StateUnknown) {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			actual, err := sim.adapter.ReadCommissionDraft(context.Background(), sim.plan, "")
			if err != nil || !ordermoney.CommissionDraftMatches(sim.plan, actual) {
				t.Fatalf("actual=%+v err=%v", actual, err)
			}
			body := <-sim.captured
			if body["applicable"] != false || body["rewardType"] != "None" || body["syncId"] != nil {
				t.Fatalf("unsafe body=%+v", body)
			}
			positions := body["positions"].([]any)
			position := positions[0].(map[string]any)
			if position["reward"] != float64(-500) || sim.writes.Load() != 1 {
				t.Fatalf("reward=%v writes=%d", position["reward"], sim.writes.Load())
			}
			if _, err = sim.adapter.WriteCommissionDraft(context.Background(), sim.plan, func(context.Context) (bool, error) { return true, nil }); !errors.Is(err, ErrOrderIntent) || sim.writes.Load() != 1 {
				t.Fatal("one-use intent replayed")
			}
		})
	}
}

func TestCommissionAdapterRefusesDuplicateStaleAndRevokedWrites(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"duplicates", "preexisting", "wrong contract", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			sim := newCommissionSimulator(t, mode)
			if mode == "preexisting" {
				sim.created.Store(true)
			}
			receipt, err := sim.adapter.WriteCommissionDraft(context.Background(), sim.plan, func(context.Context) (bool, error) { return mode != "revoked", nil })
			if err == nil || receipt.State != orders.StateRejected || sim.writes.Load() != 0 {
				t.Fatalf("receipt=%+v err=%v writes=%d", receipt, err, sim.writes.Load())
			}
		})
	}
}

func TestCommissionAdapterReadbackCannotConfirmAppliedOrChangedDraft(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"applied", "wrong reward"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			sim := newCommissionSimulator(t, mode)
			sim.created.Store(true)
			actual, err := sim.adapter.ReadCommissionDraft(context.Background(), sim.plan, "")
			if err != nil {
				t.Fatal(err)
			}
			if ordermoney.CommissionDraftMatches(sim.plan, actual) {
				t.Fatal("unreviewed native changes matched")
			}
		})
	}
}
