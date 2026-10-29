//go:build integration

package catalogsync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
)

func TestSecurityMoyCreationProvesAbsenceByExactReviewedMarker(t *testing.T) {
	t.Parallel()
	scope := catalog.Scope{Tenant: "synthetic-create-security", MoyskladAccount: "10000000-0000-0000-0000-000000000001", OzonAccount: "77"}
	var reads atomic.Int32
	server := securityCreationAbsenceServer(t, scope.MoyskladAccount, &reads)
	defer server.Close()
	client, err := moysklad.New(moysklad.Options{BaseURL: server.URL, Token: "synthetic-security-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := moysklad.NewCatalogClient(client)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := Enrollment{ID: "reviewed-create", Source: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon, ID: scope.OzonAccount}, ID: "101"},
		Target: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderMoySklad, ID: scope.MoyskladAccount}, EntityType: "product"},
		Create: &Creation{ID: "10000000-0000-0000-0000-000000000003", ExternalCode: "styk-reviewed-create", Values: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Reviewed synthetic product")}}}
	snapshot, err := readCreationTarget(t.Context(), reader, scope, enrollment)
	if err != nil || !snapshot.Complete || len(snapshot.Products) != 0 || reads.Load() == 0 {
		t.Fatalf("reviewed create could not prove exact target absence: complete=%v products=%d marker_reads=%d error=%v", snapshot.Complete, len(snapshot.Products), reads.Load(), err)
	}
}

func securityCreationAbsenceServer(t *testing.T, account string, reads *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body any = map[string]string{"accountId": account}
		if r.URL.Path != "/context/employee" {
			if r.Method != http.MethodGet || r.URL.Path != "/entity/product" || !strings.Contains(r.URL.Query().Get("filter"), "externalCode=styk-reviewed-create") {
				t.Error("creation absence lookup was not restricted to the reviewed durable marker")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			reads.Add(1)
			limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
			if err != nil || limit < 1 || limit > 2 {
				t.Error("creation absence lookup was not bounded")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body = map[string]any{"meta": map[string]int{"size": 0, "offset": 0, "limit": limit}, "rows": []any{}}
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode synthetic creation lookup response: %v", err)
		}
	}))
}
