package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

func TestOrderMethodsV2TraversesCursorsAndPreservesExactIdentities(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != orderMethodsPath {
			t.Error("unexpected endpoint")
			return
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if string(body["filter"]) != `{"status":["NEW","EDITED","ACTIVE","DISABLED","WAITING","BROKEN"]}` {
			t.Error("status default could omit active methods")
		}
		if _, exists := body["offset"]; exists {
			t.Error("retired v1 offset sent")
		}
		if string(body["limit"]) != "100" || string(body["sort_dir"]) != `"ASC"` {
			t.Error("invalid v2 pagination request")
		}
		var cursor string
		if err := json.Unmarshal(body["cursor"], &cursor); err != nil {
			t.Error(err)
			return
		}
		switch calls.Add(1) {
		case 1:
			if cursor != "" {
				t.Error("initial cursor not empty")
			}
			writeTestResponse(t, w, `{"delivery_methods":[{"id":9223372036854775806,"warehouse_id":9007199254740993,"is_express":true}],"has_next":true,"cursor":"opaque+/=cursor"}`)
		case 2:
			if cursor != "opaque+/=cursor" {
				t.Error("provider cursor altered")
			}
			writeTestResponse(t, w, `{"delivery_methods":[{"id":9223372036854775807,"warehouse_id":9007199254740995,"is_express":false}],"has_next":false}`)
		default:
			t.Error("unexpected extra request")
		}
	}))
	defer server.Close()
	methods, err := newOrderTestClient(t, server, OrderReadOptions{}).readOrderMethods(context.Background())
	if err != nil || len(methods) != 2 || calls.Load() != 2 {
		t.Fatalf("cursor traversal failed: %+v %v", methods, err)
	}
	if methods[0].ID != "9223372036854775806" || methods[0].WarehouseID != "9007199254740993" || methods[1].ID != "9223372036854775807" || methods[1].WarehouseID != "9007199254740995" {
		t.Fatal("int64 identity precision lost")
	}
}

func TestOrderMethodsV2RejectsIncompleteOrAmbiguousPagination(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		pages []string
	}{
		{"missing cursor", []string{`{"delivery_methods":[{"id":1,"warehouse_id":7}],"has_next":true}`}},
		{"empty cursor", []string{`{"delivery_methods":[{"id":1,"warehouse_id":7}],"has_next":true,"cursor":""}`}},
		{"null cursor", []string{`{"delivery_methods":[{"id":1,"warehouse_id":7}],"has_next":true,"cursor":null}`}},
		{"missing has_next", []string{`{"delivery_methods":[]}`}},
		{"missing array", []string{`{"has_next":false}`}},
		{"legacy wrapper", []string{`{"result":[],"has_next":false}`}},
		{"null array", []string{`{"delivery_methods":null,"has_next":false}`}},
		{"nonprogress empty page", []string{`{"delivery_methods":[],"has_next":true,"cursor":"next"}`}},
		{"repeated cursor", []string{`{"delivery_methods":[{"id":1,"warehouse_id":7}],"has_next":true,"cursor":"same"}`, `{"delivery_methods":[{"id":2,"warehouse_id":7}],"has_next":true,"cursor":"same"}`}},
		{"cursor cycle", []string{`{"delivery_methods":[{"id":1,"warehouse_id":7}],"has_next":true,"cursor":"a"}`, `{"delivery_methods":[{"id":2,"warehouse_id":7}],"has_next":true,"cursor":"b"}`, `{"delivery_methods":[{"id":3,"warehouse_id":7}],"has_next":true,"cursor":"a"}`}},
		{"duplicate cross page identity", []string{`{"delivery_methods":[{"id":1,"warehouse_id":7}],"has_next":true,"cursor":"next"}`, `{"delivery_methods":[{"id":1,"warehouse_id":8}],"has_next":false}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := int(calls.Add(1)) - 1
				if index >= len(tc.pages) {
					t.Error("followed invalid cursor")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				writeTestResponse(t, w, tc.pages[index])
			}))
			defer server.Close()
			methods, err := newOrderTestClient(t, server, OrderReadOptions{}).readOrderMethods(context.Background())
			if err == nil || len(methods) != 0 || int(calls.Load()) != len(tc.pages) {
				t.Fatalf("partial set escaped: methods=%v err=%v calls=%d", methods, err, calls.Load())
			}
		})
	}
}

func TestOrderMethodsV2BoundsPagesAndRows(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		count     int
		terminal  bool
		wantError bool
	}{
		{"ten pages complete", 100, true, false}, {"eleventh page forbidden", 100, false, true}, {"oversized page", 101, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page := calls.Add(1)
				rows := make([]string, tc.count)
				for index := range rows {
					rows[index] = fmt.Sprintf(`{"id":%d,"warehouse_id":7}`, int(page)*1000+index)
				}
				terminal := tc.terminal && (page == 10 || tc.count > 100)
				writeTestResponse(t, w, fmt.Sprintf(`{"delivery_methods":[%s],"has_next":%t,"cursor":"page-%d"}`, strings.Join(rows, ","), !terminal, page))
			}))
			defer server.Close()
			methods, err := newOrderTestClient(t, server, OrderReadOptions{}).readOrderMethods(context.Background())
			if (err != nil) != tc.wantError || calls.Load() > 10 {
				t.Fatalf("page bound incorrect: count=%d calls=%d err=%v", len(methods), calls.Load(), err)
			}
			if tc.wantError && len(methods) > 0 {
				t.Fatal("partial coverage returned as usable methods")
			}
			if !tc.wantError && len(methods) != 1000 {
				t.Fatal("complete bounded set lost")
			}
			if tc.name == "eleventh page forbidden" && !errors.Is(err, orders.ErrBlocked) {
				t.Fatal("page-budget exhaustion not explicit")
			}
		})
	}
}

func TestOrderMethodsV2EveryPageUsesScopedProviderAdmission(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := calls.Add(1)
		writeTestResponse(t, w, fmt.Sprintf(`{"delivery_methods":[{"id":%d,"warehouse_id":7}],"has_next":%t,"cursor":"next"}`, page, page == 1))
	}))
	defer server.Close()
	client := newOrderTestClient(t, server, OrderReadOptions{})
	store := &labelAdmissionStore{}
	gate, err := providercall.New(store, providercall.Options{})
	if err != nil {
		t.Fatal(err)
	}
	scope := orderTestScope()
	account := providercall.Scope{Tenant: scope.Tenant, Provider: "ozon", Account: scope.OzonAccount, CredentialRevision: 1}
	if err = client.read.Coordinate(gate, account); err != nil {
		t.Fatal(err)
	}
	methods, err := client.readOrderMethods(context.Background())
	if err != nil || len(methods) != 2 || store.started != 2 || len(store.attempts) != 2 {
		t.Fatalf("pagination bypassed account admission: %v", err)
	}
	for _, attempt := range store.attempts {
		if attempt.Scope != account || attempt.IsWrite {
			t.Fatal("method page changed budget scope or became mutation")
		}
	}
}
