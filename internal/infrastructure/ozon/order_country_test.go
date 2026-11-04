package ozon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestCountryPayloadUsesSKUWithoutInventingReadback(t *testing.T) {
	t.Parallel()
	command := orders.Command{ID: "country", Scope: orderTestScope(), PostingNumber: "posting", Action: orders.ActionCountrySet, PostingAction: &orders.PostingAction{PackingTargetFingerprint: strings.Repeat("a", 64), Parameters: orders.ActionParameters{CountrySKU: "9007199254740993", CountryISOCode: "NO", CountryConfirmed: true}}}
	payload, err := countrySetPayload(command)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(payload)
	if err != nil || !strings.Contains(string(raw), `"product_id":9007199254740993`) {
		t.Fatal("SKU precision lost")
	}
	client := &OrdersClient{}
	receipt, err := client.countrySetReceipt(command, []byte(`{"product_id":9007199254740993,"is_gtd_needed":false}`), 200)
	if err != nil || receipt.State != orders.StateAcknowledged || receipt.CountryEvidence.CountryValueObserved || !receipt.CountryEvidence.GTDNeeded.IsKnown() || *receipt.CountryEvidence.GTDNeeded.Value {
		t.Fatal("ack fabricated country readback or missing boolean", err)
	}
	receipt, err = client.countrySetReceipt(command, []byte(`{"product_id":1001,"is_gtd_needed":true}`), 200)
	if err == nil || receipt.State != orders.StateUnknown {
		t.Fatal("unrelated seller card ID acknowledged")
	}
}
