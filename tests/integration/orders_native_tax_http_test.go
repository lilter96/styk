//go:build integration

package integration

import (
	"encoding/json"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersNativeHTTPPreservesObservedIncludedTaxThroughDemandAndReturn(t *testing.T) {
	f := newNativeMovementHTTPFixtureWithTax(t, true)
	for _, posted := range []bool{false, true} {
		event := "included-tax-draft-event"
		if posted {
			event = "included-tax-shipment-event"
		}
		preview := f.proposal(t, orders.ActionDemandCreate, posted, event, "")
		f.approve(t, preview)
		f.run(t, orders.StateAcknowledged)
		f.run(t, orders.StateReconciled)
	}
	demand := f.latestWork(t)
	returned := f.proposal(t, orders.ActionSalesReturnCreate, true, "included-tax-return-event", demand.ID)
	f.approve(t, returned)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	if f.native.writes.Load() != 3 {
		t.Fatal("native tax path duplicated a document")
	}
	f.native.mu.Lock()
	defer f.native.mu.Unlock()
	for _, doc := range f.native.documents {
		line := doc.body["positions"].([]any)[0].(map[string]any)
		if doc.body["vatIncluded"] != true || doc.body["vatEnabled"] != true || line["vat"] != json.Number("20") || line["vatEnabled"] != true || line["price"] != json.Number("1234") {
			t.Fatal("observed native percentage or gross price was changed")
		}
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action IN ('demand_create','salesreturn_create') AND state='RECONCILED' AND dispatch_count=1`, 3)
}
