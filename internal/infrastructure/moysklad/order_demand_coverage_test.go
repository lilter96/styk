package moysklad

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type demandCoverageCase struct {
	name        string
	rows        int
	headerNull  bool
	visibility  func(int) string
	mutate      func(*orderSimulator, int, int, map[string]any)
	parentDrift bool
	pageError   bool
	complete    bool
	linked      int
}

func TestNativeDemandAccountCoverageRequiresCompleteStableVisibleCollection(t *testing.T) {
	t.Parallel()
	for _, tc := range nativeDemandCoverageCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			simulator, client, parent := nativeDemandCoverageFixture(t, tc)
			coverage, err := client.ReadNativeDemandCoverage(context.Background(), parent)
			if (err == nil) != tc.complete || coverage.Complete != tc.complete {
				t.Fatalf("complete=%v err=%v", coverage.Complete, err)
			}
			if tc.complete && (coverage.AccountRows != tc.rows || len(coverage.DemandIDs) != tc.linked || coverage.ParentRevision != parent.Revision) {
				t.Fatalf("wrong exact scoped coverage: %+v", coverage)
			}
			if parent.LinkedDemandIDs.IsKnown() || simulator.writes.Load() != 0 {
				t.Fatal("derived coverage mutated original header or wrote externally")
			}
		})
	}
}

func nativeDemandCoverageCases() []demandCoverageCase {
	return []demandCoverageCase{
		{name: "empty full account", complete: true},
		{name: "null header is separate from exhaustive empty evidence", headerNull: true, complete: true},
		{name: "draft linked to exact parent", rows: 1, linked: 1, complete: true},
		{name: "other parent is unrelated", rows: 1, complete: true, mutate: demandCoverageOtherParent},
		{name: "missing optional parent link", rows: 1, complete: true, mutate: demandCoverageMissingParent},
		{name: "null optional parent link", rows: 1, complete: true, mutate: demandCoverageNullParent},
		{name: "every page including final short page", rows: nativeDemandPageSize + 1, linked: nativeDemandPageSize + 1, complete: true},
		{name: "own visibility cannot prove empty", visibility: func(int) string { return "OWN" }},
		{name: "missing permissions cannot prove empty", visibility: func(int) string { return "" }},
		{name: "visibility revoked after scan", visibility: func(n int) string {
			if n > 1 {
				return "OWN"
			}
			return "ALL"
		}},
		{name: "missing rows", mutate: func(_ *orderSimulator, _, _ int, body map[string]any) { delete(body, "rows") }},
		{name: "null rows", mutate: func(_ *orderSimulator, _, _ int, body map[string]any) { body["rows"] = nil }},
		{name: "missing meta", mutate: func(_ *orderSimulator, _, _ int, body map[string]any) { delete(body, "meta") }},
		{name: "truncated page", rows: 1, mutate: func(_ *orderSimulator, _, _ int, body map[string]any) { body["rows"] = []any{} }},
		{name: "foreign account", rows: 1, mutate: demandCoverageForeignAccount},
		{name: "wrong native identity", rows: 1, mutate: demandCoverageWrongID},
		{name: "foreign customerorder reference", rows: 1, mutate: demandCoverageForeignParent},
		{name: "changed links between scans", rows: 1, mutate: func(s *orderSimulator, pass, offset int, body map[string]any) {
			if pass == 2 {
				demandCoverageOtherParent(s, pass, offset, body)
			}
		}},
		{name: "duplicate IDs", rows: 2, mutate: func(_ *orderSimulator, _, _ int, body map[string]any) {
			rows := body["rows"].([]any)
			rows[1] = rows[0]
		}},
		{name: "wrong offset", mutate: func(_ *orderSimulator, _, _ int, body map[string]any) { body["meta"].(map[string]any)["offset"] = 1 }},
		{name: "over shared collection bound", mutate: func(_ *orderSimulator, _, _ int, body map[string]any) {
			body["meta"].(map[string]any)["size"] = maxRows + 1
		}},
		{name: "changed total between pages", rows: nativeDemandPageSize + 1, mutate: func(_ *orderSimulator, _, offset int, body map[string]any) {
			if offset > 0 {
				body["meta"].(map[string]any)["size"] = nativeDemandPageSize + 2
			}
		}},
		{name: "parent changed during traversal", parentDrift: true},
		{name: "page permission error", pageError: true},
	}
}

func nativeDemandCoverageFixture(t *testing.T, tc demandCoverageCase) (*orderSimulator, *OrderClient, orders.TargetObservation) {
	t.Helper()
	s, client := newOrderSimulator(t, "ok")
	var permissions, pass atomic.Int64
	s.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/remap/1.2/")
		if r.Method != http.MethodGet {
			t.Error("coverage attempted mutation")
			w.WriteHeader(403)
			return
		}
		if path == "context/employee" {
			n := permissions.Add(1)
			view := "ALL"
			if tc.visibility != nil {
				view = tc.visibility(int(n))
			}
			s.reply(w, map[string]any{"accountId": client.scope.MoyskladAccount, "permissions": map[string]any{"demand": map[string]string{"view": view}}})
			return
		}
		if path == "entity/demand" {
			nativeDemandCoveragePage(t, s, tc, &pass, w, r)
			return
		}
		if path == "entity/customerorder/"+orderTestID(9) {
			row := s.document()
			if tc.headerNull {
				row["demands"] = nil
			}
			if tc.parentDrift && pass.Load() > 0 {
				row["updated"] = "2026-10-30 11:05:00.000"
			}
			s.reply(w, row)
			return
		}
		s.serveHTTP(w, r)
	})
	correlation := s.command.Correlation
	correlation.CustomerOrderID = orderTestID(9)
	parent, err := client.ReadCustomerOrder(context.Background(), correlation)
	if err != nil {
		t.Fatal(err)
	}
	permissions.Store(0)
	return s, client, parent
}

func nativeDemandCoveragePage(t *testing.T, s *orderSimulator, tc demandCoverageCase, pass *atomic.Int64, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if tc.pageError {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	query := r.URL.Query()
	if query.Get("filter") != "" || query.Get("order") != "id,asc" || query.Get("limit") != strconv.Itoa(nativeDemandPageSize) || len(query) != 3 {
		t.Error("coverage query is filtered, unsupported, or unbounded")
	}
	offset, err := strconv.Atoi(query.Get("offset"))
	if err != nil {
		t.Error(err)
		w.WriteHeader(400)
		return
	}
	if offset == 0 {
		pass.Add(1)
	}
	rows := make([]any, 0, min(tc.rows-offset, nativeDemandPageSize))
	for i := offset; i < min(tc.rows, offset+nativeDemandPageSize); i++ {
		row := s.reference("entity/demand", "demand", 100+i)
		row["id"], row["accountId"], row["applicable"] = orderTestID(100+i), s.command.Scope.MoyskladAccount, false
		row["customerOrder"] = s.reference("entity/customerorder", "customerorder", 9)
		rows = append(rows, row)
	}
	body := map[string]any{"meta": map[string]any{"size": tc.rows, "limit": nativeDemandPageSize, "offset": offset}, "rows": rows}
	if tc.mutate != nil {
		tc.mutate(s, int(pass.Load()), offset, body)
	}
	s.reply(w, body)
}

func demandCoverageOtherParent(s *orderSimulator, _, _ int, body map[string]any) {
	body["rows"].([]any)[0].(map[string]any)["customerOrder"] = s.reference("entity/customerorder", "customerorder", 99)
}
func demandCoverageMissingParent(_ *orderSimulator, _, _ int, body map[string]any) {
	delete(body["rows"].([]any)[0].(map[string]any), "customerOrder")
}
func demandCoverageNullParent(_ *orderSimulator, _, _ int, body map[string]any) {
	body["rows"].([]any)[0].(map[string]any)["customerOrder"] = nil
}
func demandCoverageForeignAccount(_ *orderSimulator, _, _ int, body map[string]any) {
	body["rows"].([]any)[0].(map[string]any)["accountId"] = orderTestID(99)
}
func demandCoverageWrongID(_ *orderSimulator, _, _ int, body map[string]any) {
	body["rows"].([]any)[0].(map[string]any)["id"] = orderTestID(99)
}
func demandCoverageForeignParent(_ *orderSimulator, _, _ int, body map[string]any) {
	body["rows"].([]any)[0].(map[string]any)["customerOrder"] = map[string]any{"meta": map[string]string{"type": "customerorder", "href": "https://foreign.example/entity/customerorder/" + orderTestID(9)}}
}

func TestNativeDemandPreflightRechecksAccountCoverageInsteadOfFrozenEmpty(t *testing.T) {
	t.Parallel()
	s, client, parent := nativeDemandCoverageFixture(t, demandCoverageCase{rows: 1})
	now := time.Now().UTC()
	command := orders.Command{Scope: client.scope, Action: orders.ActionDemandCreate, Correlation: parent.Correlation, ExpectedTargetRevision: parent.Revision, MerchantIntent: &orders.MerchantIntent{}, NativeMovement: &orders.NativeMovement{DemandCoverage: &orders.NativeDemandCoverage{Scope: client.scope, Correlation: parent.Correlation, ParentRevision: parent.Revision, StartedAt: now.Add(-time.Second), FinishedAt: now, Visibility: "ALL", Complete: true, DemandIDs: []string{}}}}
	if err := client.nativeDemandPreflight(context.Background(), command, parent); err == nil || s.writes.Load() != 0 {
		t.Fatal("fresh foreign draft escaped the final guard", err)
	}
}
