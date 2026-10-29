package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/testpublication"
)

var catalogFixtureScope = catalog.Scope{Tenant: "fixture", MoyskladAccount: "ms", OzonAccount: "1"}

func catalogFixtureOptions(base string) Options {
	return Options{BaseURL: base, ClientID: "1", APIKey: "test-secret", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 3, PageSize: 100}
}

func catalogFixtureIdentity() catalog.Identity {
	return catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon, ID: "1"}, EntityType: "product", ID: "42", OfferID: "OFFER", SKU: "43"}
}

func catalogFixtureCommand() catalog.Command {
	return catalog.Command{ID: "command", Scope: catalogFixtureScope, MappingID: "mapping", MappingVersion: 1, PolicyRevision: "policy", Source: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderMoySklad, ID: "ms"}, EntityType: "product", ID: "source"}, Target: catalogFixtureIdentity(), Action: catalog.ActionUpdate, ExpectedSourceRevision: "source-revision", Values: map[catalog.Field]catalog.Value{catalog.FieldDescription: catalog.Text("new description")}}
}

func replyCatalogJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode fixture: %v", err)
	}
}

func catalogFixtureRead(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Header.Get("Client-Id") != "1" || r.Header.Get("Api-Key") != "test-secret" {
		t.Error("authentication headers missing")
	}
	responses := map[string]any{
		productInfoPath:        map[string]any{"items": []any{map[string]any{"id": 42, "offer_id": "OFFER", "sku": "43", "name": "old name", "price": "25.20", "currency_code": "RUB", "is_archived": false, "images": []string{}, "vat": "0", "unowned": "preserve"}}},
		catalogAttributesPath:  map[string]any{"total": "1", "result": []any{map[string]any{"id": 42, "offer_id": "OFFER", "name": "old name", "description_category_id": 7, "type_id": 8, "height": 1, "width": 2, "depth": 3, "dimension_unit": "mm", "weight": 4, "weight_unit": "g", "barcode": "", "barcodes": []string{}, "attributes": []any{map[string]any{"id": 123, "complex_id": 0, "values": []any{map[string]any{"dictionary_value_id": 0, "value": "unowned"}}}}}}},
		catalogDescriptionPath: map[string]any{"result": map[string]any{"id": 42, "offer_id": "OFFER", "description": "old description"}},
		catalogSchemaPath:      map[string]any{"result": []CategoryAttribute{{ID: 4180, Type: "string"}, {ID: 4191, Type: "multiline"}}},
	}
	response, ok := responses[r.URL.Path]
	if ok {
		replyCatalogJSON(t, w, response)
	}
	return ok
}

func fixtureCatalogClient(t *testing.T, handler http.HandlerFunc) *CatalogClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewSyntheticCatalogClient(catalogFixtureOptions(server.URL), catalogFixtureScope)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func fixtureCatalogSnapshot(t *testing.T, client *CatalogClient) catalog.Snapshot {
	t.Helper()
	snapshot, err := client.ReadCatalog(context.Background(), catalog.ReadRequest{Scope: catalogFixtureScope, Account: catalogFixtureIdentity().Account, Identities: []catalog.Identity{catalogFixtureIdentity()}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestCatalogDetailedReadPreservesFieldStatesAndUnownedData(t *testing.T) {
	t.Parallel()
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !catalogFixtureRead(t, w, r) {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	snapshot := fixtureCatalogSnapshot(t, client)
	if !snapshot.Complete || len(snapshot.Products) != 1 {
		t.Fatalf("incomplete snapshot: %+v", snapshot)
	}
	product := snapshot.Products[0]
	if product.Identity != catalogFixtureIdentity() || product.Lifecycle != catalog.LifecycleActive || product.Revision == "" {
		t.Fatalf("identity or revision missing: %+v", product)
	}
	if product.Fields[catalog.FieldBarcode].State != catalog.StateEmpty || product.Fields[catalog.FieldImages].State != catalog.StateEmpty {
		t.Fatal("empty provider values were lost")
	}
	if product.Fields[catalog.FieldParent].State != catalog.StateUnknown || product.Fields[catalog.FieldUnit].State != catalog.StateUnsupported {
		t.Fatal("unsupported and unknown values collapsed")
	}
	name, err := product.Fields[catalog.FieldName].StringValue()
	if err != nil || name != "old name" || !strings.Contains(string(product.Extensions["ozon:info"].Data), "preserve") {
		t.Fatal("known or unowned facts lost")
	}
	again := fixtureCatalogSnapshot(t, client)
	if product.Revision != again.Products[0].Revision {
		t.Fatal("observation time changed fact revision")
	}
}

func TestCatalogPatchIsMinimalSingleUseAndAsync(t *testing.T) {
	t.Parallel()
	var writes, authorizations atomic.Int32
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if catalogFixtureRead(t, w, r) {
			return
		}
		if r.URL.Path != catalogPatchPath {
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		writes.Add(1)
		assertCatalogPatch(t, r)
		replyCatalogJSON(t, w, map[string]any{"task_id": 91})
	})
	command := catalogFixtureCommand()
	command.Values[catalog.FieldName] = catalog.Text("new name")
	command.ExpectedTargetRevision = fixtureCatalogSnapshot(t, client).Products[0].Revision
	if err := client.BindCatalogIntent(command, func(context.Context) (bool, error) { authorizations.Add(1); return true, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteCatalog(context.Background(), command)
	if err != nil || receipt.State != catalog.ReceiptAccepted || receipt.TaskID != "91" {
		t.Fatalf("acceptance: %+v %v", receipt, err)
	}
	if _, err := client.WriteCatalog(context.Background(), command); !errors.Is(err, ErrCatalogWritesDisabled) {
		t.Fatalf("repeat accepted: %v", err)
	}
	if writes.Load() != 1 || authorizations.Load() != 1 {
		t.Fatalf("writes=%d authorizations=%d", writes.Load(), authorizations.Load())
	}
}

func assertCatalogPatch(t *testing.T, r *http.Request) {
	t.Helper()
	var body struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error(err)
		return
	}
	if len(body.Items) != 1 {
		t.Errorf("wrong item count: %d", len(body.Items))
		return
	}
	if len(body.Items[0]) != 2 || string(body.Items[0]["offer_id"]) != `"OFFER"` {
		t.Errorf("patch overwrites unselected data: %+v", body)
	}
	var attributes []CatalogAttribute
	if err := json.Unmarshal(body.Items[0]["attributes"], &attributes); err != nil {
		t.Error(err)
		return
	}
	if len(attributes) != 2 || attributes[0].ID != 4180 || attributes[1].ID != 4191 {
		t.Errorf("wrong selected attributes: %+v", attributes)
	}
}

func TestCatalogWritesUnknownNeverRetry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
	}{{"server", 500, `{}`}, {"rate limit", 429, `{}`}, {"redirect", 307, `{}`}, {"malformed", 200, `{`}, {"wrong envelope", 200, `{"result":{"task_id":91}}`}, {"zero task", 200, `{"task_id":0}`}, {"oversized", 200, strings.Repeat("x", (1<<20)+1)}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var writes atomic.Int32
			client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
				if catalogFixtureRead(t, w, r) {
					return
				}
				writes.Add(1)
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Error(err)
				}
			})
			command := catalogFixtureCommand()
			command.ExpectedTargetRevision = fixtureCatalogSnapshot(t, client).Products[0].Revision
			if err := client.BindCatalogIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
				t.Fatal(err)
			}
			receipt, err := client.WriteCatalog(context.Background(), command)
			if !errors.Is(err, ErrCatalogWriteUnknown) || receipt.State != catalog.ReceiptUnknown || writes.Load() != 1 {
				t.Fatalf("receipt=%+v err=%v writes=%d", receipt, err, writes.Load())
			}
		})
	}
}

func TestCatalogIntentBindsExactCommandAndFreshRevision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*catalog.Command)
		allow  bool
	}{{"changed field", func(c *catalog.Command) { c.Values[catalog.FieldDescription] = catalog.Text("changed") }, true}, {"stale target", func(c *catalog.Command) { c.ExpectedTargetRevision = "stale" }, true}, {"authorization withdrawn", func(*catalog.Command) {}, false}, {"clear unsupported", func(c *catalog.Command) { c.Values[catalog.FieldDescription] = catalog.Clear() }, true}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var writes atomic.Int32
			client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
				if !catalogFixtureRead(t, w, r) {
					writes.Add(1)
				}
			})
			command := catalogFixtureCommand()
			command.ExpectedTargetRevision = fixtureCatalogSnapshot(t, client).Products[0].Revision
			if err := client.BindCatalogIntent(command, func(context.Context) (bool, error) { return tc.allow, nil }); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&command)
			if _, err := client.WriteCatalog(context.Background(), command); err == nil {
				t.Fatal("unsafe mutation accepted")
			}
			if writes.Load() != 0 {
				t.Fatal("unsafe command dispatched")
			}
		})
	}
}

func TestCatalogScopeAndHostGatesRemainClosed(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_CATALOG", "true")
	for _, base := range []string{BaseURL, "https://127.0.0.1:1234", "http://localhost:1234", "http://example.test:1234"} {
		if _, err := NewSyntheticCatalogClient(catalogFixtureOptions(base), catalogFixtureScope); err == nil {
			t.Fatalf("synthetic allowed %s", base)
		}
	}
	if _, err := NewControlledTestCatalogClient(catalogFixtureScope, "key"); !errors.Is(err, ErrCatalogWritesDisabled) {
		t.Fatalf("generic real host enabled: %v", err)
	}
	stockScope := testpublication.Scope()
	allowed := catalog.Scope{Tenant: stockScope.Tenant, MoyskladAccount: stockScope.MoyskladAccount, OzonAccount: stockScope.OzonAccount}
	if _, err := NewControlledTestCatalogClient(allowed, "key"); err != nil {
		t.Fatalf("fixed authorized scope blocked: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected network") }))
	defer server.Close()
	reader, err := NewCatalogClient(catalogFixtureOptions(server.URL), catalogFixtureScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.BindCatalogIntent(catalogFixtureCommand(), func(context.Context) (bool, error) { return true, nil }); !errors.Is(err, ErrCatalogWritesDisabled) {
		t.Fatalf("ordinary reader became writer: %v", err)
	}
}
