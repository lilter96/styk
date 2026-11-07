package moysklad

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNativeChangesPollingUsesOfficialUpdatedWindowWithoutWebhooks(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)
	simulator.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/context/employee") {
			simulator.serveHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/entity/customerorder") {
			t.Error("polling requested webhook or mutation", r.Method, r.URL.Path)
			w.WriteHeader(403)
			return
		}
		q := r.URL.Query()
		if q.Get("filter") != "updated>=2026-11-06 15:00:00.000;updated<2026-11-06 15:05:00.000" || q.Get("order") != "updated,asc;id,asc" || q.Get("offset") != "0" || q.Get("limit") != "2" {
			t.Error("wrong bounded provider query", q)
		}
		body := map[string]any{"meta": map[string]int{"size": 3, "offset": 0, "limit": 2}, "rows": []any{
			map[string]any{"id": orderTestID(9), "accountId": client.scope.MoyskladAccount, "updated": "2026-11-06 15:01:00.000", "name": "private"},
			map[string]any{"id": orderTestID(10), "accountId": client.scope.MoyskladAccount, "updated": "2026-11-06 15:01:00.000"},
		}}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Error(err)
		}
	})
	page, err := client.ReadNativeOrderChanges(context.Background(), client.scope, from, to, 2, "")
	if err != nil || len(page.Changes) != 2 || page.NextCursor != "2" {
		t.Fatalf("bounded polling page=%+v err=%v", page, err)
	}
	raw, err := json.Marshal(page)
	if err != nil || strings.Contains(string(raw), "private") {
		t.Fatal("polling hints leaked document fields", err)
	}
}

func TestNativeChangesRejectSchemaDriftAndUnorderedTimestampTies(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	zone := time.FixedZone("MSK", 10800)
	account := orderTestID(1)
	tests := []struct {
		name  string
		rows  []map[string]any
		meta  map[string]int
		allow bool
	}{
		{name: "timestamp ties ordered by exact UUID", rows: []map[string]any{{"id": orderTestID(9), "accountId": account, "updated": "2026-11-06 15:01:00"}, {"id": orderTestID(10), "accountId": account, "updated": "2026-11-06 15:01:00.000"}}, allow: true},
		{name: "duplicate identity", rows: []map[string]any{{"id": orderTestID(9), "accountId": account, "updated": "2026-11-06 15:01:00"}, {"id": orderTestID(9), "accountId": account, "updated": "2026-11-06 15:02:00"}}},
		{name: "foreign account", rows: []map[string]any{{"id": orderTestID(9), "accountId": "foreign", "updated": "2026-11-06 15:01:00"}}},
		{name: "missing updated", rows: []map[string]any{{"id": orderTestID(9), "accountId": account}}},
		{name: "outside fixed upper bound", rows: []map[string]any{{"id": orderTestID(9), "accountId": account, "updated": "2026-11-06 15:05:00"}}},
		{name: "old row", rows: []map[string]any{{"id": orderTestID(9), "accountId": account, "updated": "2026-11-06 14:59:59"}}},
		{name: "timestamp reversal", rows: []map[string]any{{"id": orderTestID(9), "accountId": account, "updated": "2026-11-06 15:02:00"}, {"id": orderTestID(10), "accountId": account, "updated": "2026-11-06 15:01:00"}}},
		{name: "wrong offset", rows: []map[string]any{}, meta: map[string]int{"size": 0, "offset": 1, "limit": 2}},
		{name: "false terminal page", rows: []map[string]any{}, meta: map[string]int{"size": 1, "offset": 0, "limit": 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			meta := tc.meta
			if meta == nil {
				meta = map[string]int{"size": len(tc.rows), "offset": 0, "limit": 2}
			}
			raw, err := json.Marshal(map[string]any{"meta": meta, "rows": tc.rows})
			if err != nil {
				t.Fatal(err)
			}
			scope := orderTestCommand(t).Scope
			_, err = decodeNativeChanges(raw, scope, 0, 2, from, from.Add(5*time.Minute), zone)
			if (err == nil) != tc.allow {
				t.Fatalf("admitted=%v want=%v err=%v", err == nil, tc.allow, err)
			}
		})
	}
}
