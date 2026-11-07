package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func TestCommissionAdapterPartialSalesReturnsWriteAndExactReadback(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"normal", "lost"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			sim := commissionReturnsSimulator(t, mode, "")
			receipt, err := sim.adapter.WriteCommissionDraft(context.Background(), sim.plan, func(context.Context) (bool, error) { return true, nil })
			if mode == "normal" && (err != nil || receipt.State != orders.StateAcknowledged) || mode == "lost" && (!errors.Is(err, ErrOrderUnknown) || receipt.State != orders.StateUnknown) {
				t.Fatal("unexpected outcome", receipt, err)
			}
			body := <-sim.captured
			if body["applicable"] != false || len(body["returnToCommissionerPositions"].([]any)) != 1 || body["syncId"] != nil {
				t.Fatal("native return draft contract violated")
			}
			actual, err := sim.adapter.ReadCommissionDraft(context.Background(), sim.plan, "")
			if err != nil || len(actual.Returns) != 1 || !ordermoney.CommissionDraftMatches(sim.plan, actual) || sim.plan.Accounting.Complete {
				t.Fatal("native return readback or residual lost", err)
			}
			if _, err = sim.adapter.ReadCommissionDraft(context.Background(), sim.plan, actual.DocumentID); err != nil || sim.writes.Load() != 1 {
				t.Fatal("reconciliation resent native draft", err)
			}
		})
	}
}

func TestCommissionAdapterReturnReadbackRejectsChangedOrForeignPositions(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"reward", "foreign", "null rows", "missing row"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			sim := commissionReturnsSimulator(t, "normal", mode)
			sim.created.Store(true)
			actual, err := sim.adapter.ReadCommissionDraft(context.Background(), sim.plan, orderTestID(30))
			if err == nil && ordermoney.CommissionDraftMatches(sim.plan, actual) {
				t.Fatal("changed native return was accepted", mode)
			}
			if sim.writes.Load() != 0 {
				t.Fatal("mismatched readback created another report")
			}
		})
	}
}

func commissionReturnsSimulator(t *testing.T, mode, bad string) *commissionSimulator {
	t.Helper()
	sim := newCommissionSimulator(t, mode)
	source := sim.plan.Source
	policy := sim.plan.Policy
	policy.OtherComponents, policy.ReturnTreatment = orders.CommissionPartialReview, orders.CommissionReturnsNative
	source.Rows[0].Delivery.BankCoinvestment = orders.Present(orders.Decimal("2"))
	source.Rows[0].Delivery.Total = orders.Present(orders.Decimal("207"))
	a := source.Rows[0].Delivery
	a.Quantity = orders.Present(orders.Decimal("1"))
	a.Amount = orders.Present(orders.Decimal("100"))
	a.StandardFee = orders.Present(orders.Decimal("10"))
	a.Bonus = orders.Present(orders.Decimal("25"))
	a.BankCoinvestment = orders.Present(orders.Decimal("1"))
	a.Total = orders.Present(orders.Decimal("116"))
	source.Rows[0].Return = a
	var err error
	source.Fingerprint, err = ordermoney.CommissionSourceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	plan, issues := ordermoney.PlanCommissionDraft(orders.Config{Scope: sim.plan.Scope, Revision: sim.plan.ConfigRevision, Finance: &policy}, sim.plan.Credentials, source, []orders.ProductMappingRef{sim.plan.Lines[0].Mapping}, sim.plan.References, sim.plan.ID, time.Now().UTC())
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	sim.plan = plan
	sim.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/remap/1.2/")
		if r.Method == http.MethodGet && path == "entity/commissionreportin/"+orderTestID(30) {
			row := commissionReturnsDocument(t, sim)
			sim.reply(w, row)
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(path, "/returntocommissionerpositions") {
			commissionReturnsPage(t, sim, w, r, bad)
			return
		}
		sim.serve(w, r)
	})
	return sim
}

func commissionReturnsDocument(t *testing.T, sim *commissionSimulator) map[string]any {
	t.Helper()
	row := sim.document()
	for key, amount := range map[string]orders.Decimal{"sum": sim.plan.Totals.Gross, "commitentSum": sim.plan.Totals.Net} {
		number, err := ordermoney.CommissionMoneyToMinor(orders.Money{Amount: amount, Currency: "RUB"})
		if err != nil {
			t.Error(err)
			return nil
		}
		row[key] = number
	}
	row["returnToCommissionerPositions"] = map[string]any{"meta": map[string]any{"href": sim.server.URL + "/api/remap/1.2/entity/commissionreportin/" + orderTestID(30) + "/returntocommissionerpositions", "size": len(sim.plan.Returns)}}
	row["positions"] = map[string]any{"meta": map[string]any{"href": sim.server.URL + "/api/remap/1.2/entity/commissionreportin/" + orderTestID(30) + "/positions", "size": len(sim.plan.Lines)}}
	return row
}

func TestCommissionAdapterReturnOnlyDraftDoesNotInventSaleOrExpense(t *testing.T) {
	t.Parallel()
	sim := commissionReturnsSimulator(t, "normal", "")
	source, policy := sim.plan.Source, sim.plan.Policy
	source.Rows[0].Delivery = orders.RealizationAmounts{Presence: orders.PresenceNull}
	var err error
	source.Fingerprint, err = ordermoney.CommissionSourceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	plan, issues := ordermoney.PlanCommissionDraft(orders.Config{Scope: sim.plan.Scope, Revision: sim.plan.ConfigRevision, Finance: &policy}, sim.plan.Credentials, source, []orders.ProductMappingRef{sim.plan.Returns[0].Mapping}, sim.plan.References, sim.plan.ID, time.Now().UTC())
	if len(issues) > 0 || len(plan.Lines) != 0 || len(plan.Returns) != 1 {
		t.Fatal("return-only source was fabricated into sale", issues)
	}
	sim.plan = plan
	if _, err = sim.adapter.WriteCommissionDraft(context.Background(), plan, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	body := <-sim.captured
	if body["commissionOverhead"] != nil || len(body["positions"].([]any)) != 0 {
		t.Fatal("fake sale/expense sent")
	}
	actual, err := sim.adapter.ReadCommissionDraft(context.Background(), plan, "")
	if err != nil || !ordermoney.CommissionDraftMatches(plan, actual) || sim.writes.Load() != 1 {
		t.Fatal("return-only exact readback failed", err)
	}
}

func commissionReturnsPage(t *testing.T, sim *commissionSimulator, w http.ResponseWriter, r *http.Request, bad string) {
	t.Helper()
	rows := make([]any, 0, len(sim.plan.Returns))
	for i, line := range sim.plan.Returns {
		position, err := sim.adapter.commissionPosition(line, sim.plan.Policy)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		position["id"], position["accountId"] = orderTestID(50+i), sim.plan.Scope.MoyskladAccount
		if bad == "reward" {
			position["reward"] = json.Number("-1400")
		}
		if bad == "foreign" {
			position["accountId"] = orderTestID(99)
		}
		rows = append(rows, position)
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		t.Error(err)
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil {
		t.Error(err)
		return
	}
	if bad == "null rows" {
		sim.reply(w, map[string]any{"meta": map[string]any{"size": 1, "limit": limit, "offset": offset}, "rows": nil})
		return
	}
	if bad == "missing row" {
		rows = []any{}
	}
	sim.page(w, rows, len(sim.plan.Returns), limit, offset)
}
