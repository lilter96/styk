package ozon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestExemplarWeightAndCountRoundtripNativeWithoutConversion(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"posting_number":"p","multi_box_qty":2,"products":[{"product_id":2001,"quantity":1,"is_weight_needed":false,"exemplars":[{"exemplar_id":3,"weight":1.250,"marks":[]}]}]}`)
	snapshot, err := decodeExemplarSnapshot(raw, "p")
	if err != nil {
		t.Fatal(err)
	}
	input := &orders.ExemplarSetInput{MultiBoxQty: snapshot.MultiBoxQty, Products: []orders.ExemplarProductInput{{SKU: snapshot.Products[0].SKU, Exemplars: snapshot.Products[0].Exemplars}}}
	payload, err := exemplarSetPayload(orders.Command{PostingNumber: "p", PostingAction: &orders.PostingAction{Parameters: orders.ActionParameters{Exemplars: input}}})
	encoded, marshalErr := json.Marshal(payload)
	if err != nil || marshalErr != nil || !strings.Contains(string(encoded), `"weight":1.250`) || !strings.Contains(string(encoded), `"multi_box_qty":2`) {
		t.Fatal("native weight/count was dropped, converted or remeasured", err)
	}
}

func TestExemplarWeightMissingNullAndZeroRemainDistinct(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, field string
		presence    orders.Presence
	}{{"missing", "", orders.PresenceMissing}, {"null", `,"weight":null`, orders.PresenceNull}, {"zero", `,"weight":0`, orders.PresenceZero}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			snapshot, err := decodeExemplarSnapshot([]byte(`{"posting_number":"p","products":[{"product_id":2001,"quantity":1,"exemplars":[{"exemplar_id":3`+tc.field+`}]}]}`), "p")
			if err != nil {
				t.Fatal(err)
			}
			field := snapshot.Products[0].Exemplars[0].Weight
			if field.Presence != tc.presence {
				t.Fatalf("weight presence collapsed: %+v", field)
			}
		})
	}
}

func TestProviderWeightRejectsMalformedNumericNativeValues(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`"1.25"`, `false`, `{}`, `-1`} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			value, err := decodeExemplarWeight([]byte(raw))
			if err == nil {
				unit := orders.ExemplarInput{ID: "3", Weight: orders.Present(value)}
				if unit.Validate() == nil {
					t.Fatal("invalid native value accepted")
				}
			}
		})
	}
}
