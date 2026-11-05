//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

// Same documented wire shape as the current unassembled contract, with a
// retained original identity at BOTH generations. No buyer event is fabricated.
type ownedNestedHTTPProvider struct{ base *nestedSettlementHTTPProvider }

func (p ownedNestedHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/posting/fbs/split" && r.URL.Path != "/v3/posting/fbs/get" {
		p.base.serve(w, r)
		return
	}
	recorder := httptest.NewRecorder()
	p.base.serve(recorder, r)
	if recorder.Code != http.StatusOK {
		w.WriteHeader(recorder.Code)
		return
	}
	var body map[string]any
	if json.Unmarshal(recorder.Body.Bytes(), &body) != nil {
		w.WriteHeader(500)
		return
	}
	if r.URL.Path == "/v1/posting/fbs/split" && p.base.childSplit.Load() {
		body["postings"].([]any)[0].(map[string]any)["posting_number"] = nestedChild
	}
	if r.URL.Path == "/v3/posting/fbs/get" && p.base.childSplit.Load() {
		row := body["result"].(map[string]any)
		if row["posting_number"] == nestedChild {
			row["status"] = "awaiting_packaging"
			row["products"].([]any)[0].(map[string]any)["quantity"] = 1
		}
	}
	p.base.base.base.base.reply(w, body)
}
