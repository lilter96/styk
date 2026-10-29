//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestCatalogSecurityRealHostCatalogGateIsIndependent(t *testing.T) {
	tests := []struct {
		name, stock, catalog string
		allowed              bool
	}{
		{name: "default_off"},
		{name: "stock_alone", stock: "true"},
		{name: "catalog_alone", catalog: "true", allowed: true},
		{name: "both_enabled", stock: "true", catalog: "true", allowed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", tc.stock)
			t.Setenv("STYK_TEST_ACCOUNT_CATALOG", tc.catalog)
			scope := catalog.Scope{Tenant: testpublication.Tenant, MoyskladAccount: testpublication.MoyskladAccount, OzonAccount: testpublication.OzonAccount}
			_, err := moysklad.NewControlledTestCatalogClient(catalogSecurityMoyClient(t, moysklad.BaseURL), scope)
			if (err == nil) != tc.allowed {
				t.Errorf("MoySklad catalog constructor allowed=%v, want %v", err == nil, tc.allowed)
			}
			_, err = ozon.NewControlledTestCatalogClient(scope, "synthetic-security-token")
			if (err == nil) != tc.allowed {
				t.Errorf("Ozon catalog constructor allowed=%v, want %v", err == nil, tc.allowed)
			}
		})
	}
}

func catalogSecurityMoyClient(t *testing.T, base string) *moysklad.Client {
	t.Helper()
	client, err := moysklad.New(moysklad.Options{BaseURL: base, Token: "synthetic-security-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func catalogSecurityProviderScope() catalog.Scope {
	return catalog.Scope{Tenant: "synthetic-catalog-security", MoyskladAccount: "10000000-0000-0000-0000-000000000001", OzonAccount: "77"}
}

func catalogSecurityMoyCommand() catalog.Command {
	scope := catalogSecurityProviderScope()
	return catalog.Command{ID: "synthetic-security-command", Scope: scope, Action: catalog.ActionUpdate,
		Source: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon, ID: scope.OzonAccount}, EntityType: "product", ID: "101", OfferID: "SYNTHETIC-SECURITY"},
		Target: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderMoySklad, ID: scope.MoyskladAccount}, EntityType: "product", ID: "10000000-0000-0000-0000-000000000002"},
		Values: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Reviewed synthetic name")}, ExpectedTargetRevision: "reviewed-target", ExpectedSourceRevision: "reviewed-source"}
}

func TestCatalogSecurityMoySkladRejectsInvalidSourceBeforeBinding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*catalog.Command)
	}{
		{name: "missing_source_id", change: func(c *catalog.Command) { c.Source.ID = "" }},
		{name: "same_provider", change: func(c *catalog.Command) { c.Source = c.Target }},
		{name: "foreign_source_account", change: func(c *catalog.Command) { c.Source.Account.ID = "foreign" }},
		{name: "foreign_target_account", change: func(c *catalog.Command) { c.Target.Account.ID = "foreign" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			client, err := moysklad.NewControlledTestCatalogClient(catalogSecurityMoyClient(t, server.URL), catalogSecurityProviderScope())
			if err != nil {
				t.Fatal(err)
			}
			command := catalogSecurityMoyCommand()
			tc.change(&command)
			if catalog.ValidateCommand(command) == nil {
				t.Fatal("security fixture did not contradict the canonical command contract")
			}
			if err = client.BindCatalogIntent(command, func(context.Context) (bool, error) { return true, nil }); err == nil {
				t.Error("provider accepted an invalid source/destination intent")
			}
			if requests.Load() != 0 {
				t.Fatal("invalid intent made a provider request")
			}
		})
	}
}

func TestCatalogSecurityMoySkladRejectsUntrustedMetadataWithoutFetching(t *testing.T) {
	t.Parallel()
	var fetched atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetched.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer sink.Close()
	scope := catalogSecurityProviderScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body any = map[string]string{"accountId": scope.MoyskladAccount}
		if r.URL.Path != "/context/employee" {
			body = map[string]any{"id": catalogSecurityMoyCommand().Target.ID, "accountId": scope.MoyskladAccount, "name": "Synthetic", "meta": map[string]string{"href": sink.URL + "/entity/product/" + catalogSecurityMoyCommand().Target.ID, "type": "product"}, "images": map[string]any{"meta": map[string]string{"href": sink.URL + "/private-images"}}}
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode synthetic provider metadata: %v", err)
		}
	}))
	defer server.Close()
	client, err := moysklad.NewCatalogClient(catalogSecurityMoyClient(t, server.URL))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.ReadCatalog(t.Context(), catalog.ReadRequest{Scope: scope, Account: catalogSecurityMoyCommand().Target.Account, Identities: []catalog.Identity{catalogSecurityMoyCommand().Target}, Limit: 1})
	if err == nil || len(snapshot.Products) != 0 {
		t.Fatal("foreign-host metadata became a valid catalog identity")
	}
	if fetched.Load() != 0 {
		t.Fatal("provider metadata or image URL triggered a request")
	}
}

func TestCatalogSecurityImageMetadataRemainsUnfetched(t *testing.T) {
	t.Parallel()
	var fetched atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetched.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer sink.Close()
	scope, identity := catalogSecurityProviderScope(), catalogSecurityMoyCommand().Target
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body any = map[string]string{"accountId": scope.MoyskladAccount}
		if r.URL.Path != "/context/employee" {
			body = map[string]any{"id": identity.ID, "accountId": scope.MoyskladAccount, "name": "Synthetic", "meta": map[string]string{"href": "http://" + r.Host + "/entity/product/" + identity.ID, "type": "product"}, "images": map[string]any{"meta": map[string]any{"href": sink.URL + "/private-images", "size": 1}}}
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode synthetic image metadata: %v", err)
		}
	}))
	defer server.Close()
	client, err := moysklad.NewCatalogClient(catalogSecurityMoyClient(t, server.URL))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.ReadCatalog(t.Context(), catalog.ReadRequest{Scope: scope, Account: identity.Account, Identities: []catalog.Identity{identity}, Limit: 1})
	if err != nil || !snapshot.Complete || len(snapshot.Products) != 1 || snapshot.Products[0].Fields[catalog.FieldImages].State != catalog.StateUnknown {
		t.Fatalf("unexpanded image reference became a verified image value: products=%d error=%v", len(snapshot.Products), err)
	}
	if fetched.Load() != 0 {
		t.Fatal("otherwise valid provider metadata followed an image URL")
	}
}
