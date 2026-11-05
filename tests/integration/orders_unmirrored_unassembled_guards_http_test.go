//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersUnmirroredUnassembledMissingOriginalCannotAuthorizeNativeReserve(t *testing.T) {
	f, provider, preview := unassembledHTTPFixture(t)
	f.provider.base.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/posting/fbs/get" && provider.base.split.Load() {
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
			if err != nil {
				w.WriteHeader(400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var body struct {
				Posting string `json:"posting_number"`
			}
			if json.Unmarshal(raw, &body) != nil {
				w.WriteHeader(400)
				return
			}
			if body.Posting == "position-http-posting" {
				w.WriteHeader(404)
				return
			}
		}
		provider.serve(w, r)
	})
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StatePolling)
	f.run(t, orders.StateReconciled) // Allocation known; original retirement explicitly NOT known.
	split := f.latestWork(t)
	child := unmirroredUnassembledHTTPReview(t, f, split.Receipt.PostingNumbers[0])
	if child.Decisions[0].Ready || len(child.Decisions[0].Commands) != 0 {
		t.Fatal("generic unavailable original unlocked child reserve")
	}
	if provider.writes.Load() != 1 {
		t.Fatal("missing original replayed split")
	}
}

type retainedUnassembledHTTPProvider struct{ base *unmirroredSplitHTTPProvider }

func (p retainedUnassembledHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/posting/fbs/split" {
		p.split(w, r)
		return
	}
	if r.URL.Path != "/v3/posting/fbs/get" {
		p.base.serve(w, r)
		return
	}
	recorder := httptest.NewRecorder()
	p.base.serve(recorder, r)
	if recorder.Code != 200 {
		w.WriteHeader(recorder.Code)
		return
	}
	var payload map[string]map[string]any
	if json.Unmarshal(recorder.Body.Bytes(), &payload) != nil {
		w.WriteHeader(500)
		return
	}
	payload["result"]["status"] = "awaiting_packaging"
	p.base.base.base.reply(w, payload)
}

func (p retainedUnassembledHTTPProvider) split(w http.ResponseWriter, r *http.Request) {
	var payload map[string]json.RawMessage
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&payload) != nil {
		w.WriteHeader(400)
		return
	}
	payload["packages"] = payload["postings"]
	delete(payload, "postings")
	raw, err := json.Marshal(payload)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	cloned := r.Clone(r.Context())
	cloned.URL.Path = "/v4/posting/fbs/ship" // Reuse only the deterministic fixture's quantity validator, not HTTP.
	cloned.Body = io.NopCloser(bytes.NewReader(raw))
	recorder := httptest.NewRecorder()
	p.base.serve(recorder, cloned)
	if recorder.Code != 200 {
		w.WriteHeader(recorder.Code)
		return
	}
	p.base.base.base.reply(w, map[string]any{"parent_posting": map[string]any{"posting_number": "position-http-posting"}, "postings": []any{map[string]any{"posting_number": "split-http-child", "products": []any{map[string]any{"product_id": 2001, "quantity": 1}}}}})
}

func retainedUnmirroredUnassembled(t *testing.T) (positionHTTPFixture, *unmirroredSplitHTTPProvider, orders.Work) {
	t.Helper()
	f, base, initial := unmirroredSplitFixture(t)
	f.provider.base.server.Config.Handler = http.HandlerFunc(retainedUnassembledHTTPProvider{base}.serve)
	unit := orders.ActionQuantity{Identity: initial.Lines[0].Identity, Quantity: "1"}
	proposal := orders.ActionProposal{PostingNumber: initial.PostingNumber, Action: orders.ActionPostingSplitUnassembled, Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{unit}}, {Lines: []orders.ActionQuantity{unit}}}}}
	f.approve(t, splitMemberHTTPPreview(t, f, initial.PostingNumber, []orders.ActionProposal{proposal}))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	return f, base, f.latestWork(t)
}

func TestOrdersUnmirroredUnassembledRetainedNativeFirstWaitsForReconciliation(t *testing.T) {
	f, provider, split := retainedUnmirroredUnassembled(t)
	root := split.Command.PostingNumber
	ready := unmirroredUnassembledHTTPReview(t, f, "split-http-child")
	if !ready.Decisions[0].Ready || ready.Decisions[0].Commands[0].SplitSourceWorkID != split.ID {
		t.Fatal("pure unmirrored retained allocation did not have bounded proof")
	}
	parent := splitMemberHTTPPreview(t, f, root, nil)
	if parent.Decisions[0].Commands[0].Action != orders.ActionMirrorCreate || parent.Decisions[0].Commands[0].CustomerOrder.Positions[0].Quantity != "1" {
		t.Fatal("retained root was not independently reduced")
	}
	f.approve(t, parent)
	if next := unmirroredUnassembledHTTPReview(t, f, "split-http-child"); next.Decisions[0].Ready {
		t.Fatal("queued parent native creation was mistaken for native absence")
	}
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	completeRetainedAdmissionChild(t, f)
	if provider.writes.Load() != 1 {
		t.Fatal("native first path replayed split")
	}
}
