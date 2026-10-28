//go:build fixturelive

package moyskladfixture

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"example.com/styk/internal/application"
	"example.com/styk/internal/infrastructure/moysklad"
	"example.com/styk/internal/postgres"
)

// This opt-in check reads the existing fixture only. It is excluded from all
// normal and integration tests; the database must be a disposable local one.
func TestLiveVariantFixtureImport(t *testing.T) {
	if os.Getenv("STYK_FIXTURE_LIVE_READ_ONLY") != "yes" {
		t.Fatal("explicit read-only live fixture opt-in is required")
	}
	account, token := os.Getenv("MOYSKLAD_ACCOUNT_ID"), os.Getenv("MOYSKLAD_TOKEN")
	dbURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || (dbURL.Hostname() != "127.0.0.1" && dbURL.Hostname() != "localhost") || !strings.HasPrefix(strings.TrimPrefix(dbURL.Path, "/"), "styk_fixture_") {
		t.Fatal("a disposable local fixture database is required")
	}
	lock, err := OpenLock("../../.local/moysklad-fixtures", os.Getenv("STYK_FIXTURE_NAMESPACE"))
	if err != nil {
		t.Fatal("fixture journal unavailable")
	}
	defer lock.Close()
	m, err := lock.Load()
	if err != nil {
		t.Fatal("fixture journal invalid")
	}
	hash, err := AccountHash(account)
	if err != nil || hash != m.AccountHash {
		t.Fatal("configured account does not match fixture journal")
	}
	for _, key := range []string{"variant-characteristic", "variant-one", "variant-two", "stock-variants", "order-variant"} {
		if _, ok := findID(m, key); !ok {
			t.Fatal("required fixture operation is not verified")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c, err := NewClient(account, token, 10*time.Second, 500*time.Millisecond, 250, nil)
	if err != nil || c.ReadOnlyPreflight(ctx, account) != nil {
		t.Fatal("read-only account preflight failed")
	}
	for _, key := range []string{"variant-characteristic", "variant-one", "variant-two", "stock-variants", "order-variant"} {
		o := opByKey(t, m, key)
		if c.verify(ctx, o, o.RemoteID, m) != nil {
			t.Fatalf("independent fixture GET verification failed for %s", key)
		}
	}
	observed, err := c.PollStock(ctx, m, 5, 2*time.Second)
	if err != nil || !AllStockMatches(observed) {
		t.Fatal("variant warehouse report did not independently match")
	}
	reader, err := moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: token, Timeout: 10 * time.Second, Interval: 500 * time.Millisecond, Backoff: time.Second, MaxRetryAfter: 10 * time.Second, Attempts: 1, PageSize: 1})
	if err != nil {
		t.Fatal("read-only importer configuration invalid")
	}
	db, err := postgres.Open(ctx, dbURL.String(), 30*time.Second)
	if err != nil {
		t.Fatal("disposable database unavailable")
	}
	defer db.Pool.Close()
	if _, err = (application.Importer{Reader: reader, Store: db}).Run(ctx, account); err != nil {
		t.Fatalf("existing inventory importer failed: %v", err)
	}
	var tenant string
	if db.Pool.QueryRow(ctx, `SELECT tenant_id FROM stock_connections WHERE source='moysklad' AND account_id=$1`, account).Scan(&tenant) != nil {
		t.Fatal("test account ownership is not registered in the disposable database")
	}
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	since := until.Add(-24 * time.Hour)
	batch, err := (application.CustomerOrderImporter{Reader: reader, Store: db}).Run(ctx, tenant, account, since, until)
	if err != nil {
		t.Fatalf("existing customer-order importer failed: %v", err)
	}
	var importID int64
	if db.Pool.QueryRow(ctx, `SELECT id FROM source_imports WHERE source='moysklad' AND account_id=$1 ORDER BY id DESC LIMIT 1`, account).Scan(&importID) != nil {
		t.Fatal("inventory batch missing")
	}
	parent, _ := findID(m, "variant-parent")
	warehouse, _ := findID(m, "warehouse-a")
	var parentCount int
	if db.Pool.QueryRow(ctx, `SELECT count(*) FROM source_entities WHERE import_id=$1 AND kind='product' AND external_id=$2`, importID, parent).Scan(&parentCount) != nil || parentCount != 1 {
		t.Fatal("variant parent was not persisted as a distinct product")
	}
	aggregate, err := c.page(ctx, "report/stock/all", url.Values{"limit": {"100"}, "offset": {"0"}, "filter": {"stockMode=all;quantityMode=all"}})
	if err != nil || len(*aggregate.Rows) != *aggregate.Meta.Size {
		t.Fatal("independent aggregate report incomplete")
	}
	for _, key := range []string{"variant-one", "variant-two"} {
		o := opByKey(t, m, key)
		if o.RemoteID == parent {
			t.Fatal("variant and parent identities were collapsed")
		}
		var remote map[string]any
		if c.call(ctx, "GET", "entity/variant/"+o.RemoteID, nil, nil, &remote) != nil {
			t.Fatal("variant detail unavailable")
		}
		var importedParent, updated, name, article string
		if db.Pool.QueryRow(ctx, `SELECT parent_id,source_updated,name,article FROM source_entities WHERE import_id=$1 AND kind='variant' AND external_id=$2`, importID, o.RemoteID).Scan(&importedParent, &updated, &name, &article) != nil || importedParent != parent || updated != str(remote, "updated") || name != str(remote, "name") || article != str(remote, "article") {
			t.Fatal("variant identity, parent or source values differ from independent GET")
		}
		var physical, reserve, incoming string
		var available *string
		if db.Pool.QueryRow(ctx, `SELECT stock::text,reserve::text,in_transit::text,quantity::text FROM source_warehouse_stock_observations WHERE import_id=$1 AND kind='variant' AND external_id=$2 AND warehouse_id=$3`, importID, o.RemoteID, warehouse).Scan(&physical, &reserve, &incoming, &available) != nil {
			t.Fatal("variant warehouse observation missing")
		}
		r := observed[key+"/warehouse-a"]
		if r.Stock == nil || r.Reserve == nil || r.Incoming == nil || !equalNumber(physical, *r.Stock) || !equalNumber(reserve, *r.Reserve) || !equalNumber(incoming, *r.Incoming) || available != nil {
			t.Fatal("variant warehouse values differ or available quantity was invented")
		}
		var stock, reserved, transit, total, free *string
		if db.Pool.QueryRow(ctx, `SELECT stock::text,reserve::text,in_transit::text,quantity::text,free_stock::text FROM source_stock_observations WHERE import_id=$1 AND kind='variant' AND external_id=$2`, importID, o.RemoteID).Scan(&stock, &reserved, &transit, &total, &free) != nil {
			t.Fatal("variant aggregate stock observation missing")
		}
		matches := 0
		for _, source := range *aggregate.Rows {
			if id, ok := referenceID(source, "variant"); !ok || id != o.RemoteID {
				continue
			}
			matches++
			if !sameNullableNumber(stock, source["stock"]) || !sameNullableNumber(reserved, source["reserve"]) || !sameNullableNumber(transit, source["inTransit"]) || !sameNullableNumber(total, source["quantity"]) || !sameNullableNumber(free, source["freeStock"]) {
				t.Fatal("variant aggregate values differ from independent source report")
			}
		}
		if matches != 1 {
			t.Fatal("variant aggregate source identity was missing or duplicated")
		}
	}
	order := opByKey(t, m, "order-variant")
	var remoteOrder map[string]any
	if c.call(ctx, "GET", "entity/customerorder/"+order.RemoteID, nil, nil, &remoteOrder) != nil {
		t.Fatal("variant order detail unavailable")
	}
	p, err := c.page(ctx, "entity/customerorder/"+order.RemoteID+"/positions", url.Values{"limit": {"10"}, "offset": {"0"}})
	if err != nil || len(*p.Rows) != 1 || *p.Meta.Size != 1 {
		t.Fatal("variant order positions incomplete")
	}
	position := (*p.Rows)[0]
	variant, _ := findID(m, "variant-one")
	remoteVariant, ok := referenceID(position["assortment"], "variant")
	if !ok || remoteVariant != variant {
		t.Fatal("variant order source assortment mismatch")
	}
	var sourceUpdated, importedWarehouse, positionID, kind, assortment string
	var quantity, reserve, shipped *string
	if db.Pool.QueryRow(ctx, `SELECT o.source_updated,o.warehouse_id,p.position_id,p.assortment_kind,p.assortment_id,p.quantity::text,p.reserve::text,p.shipped::text FROM moy_order_observations o JOIN moy_order_positions p ON p.observation_id=o.id WHERE o.batch_id=$1 AND o.tenant_id=$2 AND o.account_id=$3 AND o.order_id=$4`, batch, tenant, account, order.RemoteID).Scan(&sourceUpdated, &importedWarehouse, &positionID, &kind, &assortment, &quantity, &reserve, &shipped) != nil {
		t.Fatal("persisted variant order position missing")
	}
	if sourceUpdated != str(remoteOrder, "updated") || importedWarehouse != warehouse || positionID != str(position, "id") || kind != "variant" || assortment != variant || !sameNullableNumber(quantity, position["quantity"]) || !sameNullableNumber(reserve, position["reserve"]) || !sameNullableNumber(shipped, position["shipped"]) {
		t.Fatal("persisted variant order values differ from independent source GET")
	}
	var variants, orders, positions int
	if db.Pool.QueryRow(ctx, `SELECT count(*) FROM source_entities WHERE import_id=$1 AND kind='variant'`, importID).Scan(&variants) != nil || db.Pool.QueryRow(ctx, `SELECT count(*) FROM moy_order_observations WHERE batch_id=$1`, batch).Scan(&orders) != nil || db.Pool.QueryRow(ctx, `SELECT count(*) FROM moy_order_positions p JOIN moy_order_observations o ON o.id=p.observation_id WHERE o.batch_id=$1`, batch).Scan(&positions) != nil {
		t.Fatal("import count query failed")
	}
	t.Logf("live read-only comparison passed: variants=%d customer_orders=%d positions=%d page_size=1", variants, orders, positions)
}

func sameNullableNumber(saved *string, source any) bool {
	if source == nil {
		return saved == nil
	}
	n, ok := source.(json.Number)
	return ok && saved != nil && equalNumber(*saved, n.String())
}
