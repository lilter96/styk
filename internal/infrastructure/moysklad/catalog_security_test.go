package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/testpublication"
)

func catalogBaseClient(t *testing.T, base string) *Client {
	t.Helper()
	c, err := New(Options{BaseURL: base, Token: "test-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalogClientRestrictsRealHostAndExactAuthorizedScope(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_CATALOG", "")
	scope := catalog.Scope{Tenant: testpublication.Tenant, MoyskladAccount: testpublication.MoyskladAccount, OzonAccount: testpublication.OzonAccount}
	if _, err := NewControlledTestCatalogClient(catalogBaseClient(t, BaseURL), scope); !errors.Is(err, ErrCatalogWritesDisabled) {
		t.Fatal("real catalog gate was not default-off")
	}
	t.Setenv("STYK_TEST_ACCOUNT_CATALOG", "true")
	if _, err := NewControlledTestCatalogClient(catalogBaseClient(t, BaseURL), scope); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*catalog.Scope){
		func(s *catalog.Scope) { s.Tenant = "other" },
		func(s *catalog.Scope) { s.MoyskladAccount = catalogTestAccount },
		func(s *catalog.Scope) { s.OzonAccount = "other" },
	} {
		wrong := scope
		edit(&wrong)
		if _, err := NewControlledTestCatalogClient(catalogBaseClient(t, BaseURL), wrong); !errors.Is(err, ErrCatalogWritesDisabled) {
			t.Fatal("catalog switch widened the authorized real account pair")
		}
	}
	for _, base := range []string{"https://evil.example/api/remap/1.2/", "https://api.moysklad.ru.evil.example/api/remap/1.2/", "http://api.moysklad.ru/api/remap/1.2/", "https://api.moysklad.ru/api/remap/1.2/entity/", "http://localhost/"} {
		if _, err := NewCatalogClient(catalogBaseClient(t, base)); !errors.Is(err, ErrCatalogIdentity) {
			t.Fatalf("untrusted catalog origin accepted: %s", base)
		}
	}
}

func TestCatalogReadAllowlistCannotReachFinancialOrStockDocuments(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"entity/demand", "entity/supply", "entity/enter", "entity/loss", "entity/move", "entity/paymentin", "entity/paymentout", "entity/product/../demand", "entity/product/" + catalogTestProduct + "/images", "context/companysettings/pricetype/../currency"} {
		if allowedCatalogReadPath(path) {
			t.Fatalf("catalog read path is too broad: %s", path)
		}
	}
	for _, path := range []string{"entity/product/" + catalogTestProduct, "entity/variant/" + catalogTestVariant, "entity/productfolder", "entity/uom", "entity/product/metadata/attributes", "entity/variant/metadata", "context/companysettings/pricetype"} {
		if !allowedCatalogReadPath(path) {
			t.Fatalf("documented catalog path missing: %s", path)
		}
	}
}

func TestCatalogWriterRejectsUnsupportedOrUnscopedPayloads(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		field catalog.Field
		value catalog.Value
	}{
		{"unselected empty", catalog.FieldDescription, catalog.Empty()},
		{"unknown", catalog.FieldDescription, catalog.Value{State: catalog.StateUnknown}},
		{"clear name", catalog.FieldName, catalog.Clear()},
		{"images upload", catalog.FieldImages, catalog.Value{State: catalog.StateKnown, Data: json.RawMessage(`[{"filename":"x","content":"x"}]`)}},
		{"document reference", catalog.FieldCategory, catalog.Value{State: catalog.StateKnown, Data: json.RawMessage(`{"meta":{"type":"demand","href":"https://api.moysklad.ru/api/remap/1.2/entity/demand/` + catalogTestProduct + `"}}`)}},
		{"foreign reference", catalog.FieldUnit, catalog.Value{State: catalog.StateKnown, Data: json.RawMessage(`{"meta":{"type":"uom","href":"https://evil.example/entity/uom/` + catalogTestUnit + `"}}`)}},
		{"invalid barcode", catalog.FieldBarcodes, catalog.Value{State: catalog.StateKnown, Data: json.RawMessage(`[{"unsafe":"x"}]`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newCatalogSimulator(t)
			cmd := catalogCreateCommand("product")
			cmd.Values[tc.field] = tc.value
			if err := s.writer(t).BindCatalogIntent(cmd, catalogAuthorize); err == nil {
				t.Fatal("unsafe payload admitted")
			}
			writes, _, events := s.state()
			if writes != 0 || len(events) != 0 {
				t.Fatal("invalid payload reached provider")
			}
		})
	}
}

func TestCatalogWriteRequiresFreshAuthenticatedAccount(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c, cmd := s.writer(t), catalogCreateCommand("product")
	catalogBind(t, c, cmd, catalogAuthorize)
	s.mu.Lock()
	s.account = catalogTestCreate
	s.mu.Unlock()
	r, err := c.WriteCatalog(context.Background(), cmd)
	writes, _, events := s.state()
	if !errors.Is(err, ErrCatalogIdentity) || r.State != catalog.ReceiptRejected || writes != 0 || len(events) != 1 || events[0] != http.MethodGet+" /context/employee" {
		t.Fatalf("mismatched authenticated account reached write: %+v writes=%d events=%v error=%v", r, writes, events, err)
	}
}
