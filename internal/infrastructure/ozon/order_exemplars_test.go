package ozon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestExemplarPreparationNeverInventsIDsOrLeaksPrivateResponse(t *testing.T) {
	t.Parallel()
	command := orders.Command{ID: "intent", Action: orders.ActionExemplarPrepare, PostingNumber: "posting"}
	raw := []byte(`{"posting_number":"posting","products":[{"product_id":2001,"quantity":1,"is_mandatory_mark_needed":true,"exemplars":[{"exemplar_id":9007199254740993,"gtd":"SYNTHETIC-PRIVATE","is_gtd_absent":false,"is_rnpt_absent":true,"marks":[]}]}]}`)
	r, e := (&OrdersClient{}).parseFulfillmentReceipt(command, exemplarPreparePath, raw, 200)
	if e != nil || r.State != orders.StateAcknowledged || r.PreparedExemplars.Products[0].Exemplars[0].ID != "9007199254740993" {
		t.Fatal("provider identity changed")
	}
	public, e := json.Marshal(r)
	if e != nil || containsPrivateExemplar(public) {
		t.Fatal("private declaration entered receipt")
	}
	r, e = (&OrdersClient{}).parseFulfillmentReceipt(orders.Command{ID: "set", Action: orders.ActionExemplarSet}, exemplarSetPath, nil, 200)
	if e != nil || r.State != orders.StatePolling || r.TaskID != "" {
		t.Fatal("HTTP acceptance became completed task")
	}
}

func containsPrivateExemplar(raw []byte) bool {
	return len(raw) > 0 && strings.Contains(string(raw), "SYNTHETIC-PRIVATE")
}

func TestExemplarSetUsesExactSKUAndProviderUnitID(t *testing.T) {
	t.Parallel()
	input := orders.ExemplarInput{ID: "9007199254740993", Marks: orders.Present([]orders.ExemplarMark{{Type: "mandatory_mark", Value: "SYNTHETIC\x1dMARK"}})}
	command := orders.Command{PostingNumber: "posting", PostingAction: &orders.PostingAction{Parameters: orders.ActionParameters{Exemplars: &orders.ExemplarSetInput{Products: []orders.ExemplarProductInput{{SKU: "2001", Exemplars: []orders.ExemplarInput{input}}}}}}}
	payload, err := exemplarSetPayload(command)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"product_id":2001`) || !strings.Contains(string(raw), `"exemplar_id":9007199254740993`) || strings.Contains(string(raw), "weight") {
		t.Fatal("wrong SKU, rounded unit or unsupported weight", string(raw))
	}
	if _, err = decodeExemplarSnapshot([]byte(`{"posting_number":"posting","products":[{"product_id":2001,"quantity":1,"exemplars":[{"exemplar_id":3,"unknown_private_data":"x"}]}]}`), "posting"); err == nil {
		t.Fatal("unknown exemplar field silently lost")
	}
}

func TestExemplarExplicitAbsenceSerializesClearAndFlag(t *testing.T) {
	t.Parallel()
	u := orders.ExemplarInput{ID: "9007199254740993", GTD: orders.Present(""), GTDAbsent: orders.Present(true), RNPT: orders.Present(""), RNPTAbsent: orders.Present(true)}
	payload, err := exemplarUnitPayload(u)
	if err != nil || payload["gtd"] != "" || payload["is_gtd_absent"] != true || payload["rnpt"] != "" || payload["is_rnpt_absent"] != true {
		t.Fatal("explicit seller clear was omitted", err)
	}
}

func TestExemplarMalformedPresentValueDoesNotPanicOrSerialize(t *testing.T) {
	t.Parallel()
	unit := orders.ExemplarInput{ID: "3", Marks: orders.Field[[]orders.ExemplarMark]{Presence: orders.PresenceValue}}
	if _, err := exemplarUnitPayload(unit); err == nil {
		t.Fatal("malformed present pointer serialized")
	}
}
