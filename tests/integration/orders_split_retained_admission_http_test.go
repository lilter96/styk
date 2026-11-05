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

func retainedAdmissionHTTPFixture(t *testing.T) (positionHTTPFixture, *unmirroredSplitHTTPProvider, orders.Work) {
	t.Helper()
	f, provider, split := retainedUnmirroredUnassembled(t)
	f.approve(t, splitMemberHTTPPreview(t, f, split.Command.PostingNumber, nil))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	return f, provider, f.latestWork(t)
}

func assertRetainedAdmissionNativeTotal(t *testing.T, f positionHTTPFixture, wanted int) {
	t.Helper()
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	total := 0
	for _, native := range f.provider.base.native {
		rows := native.body["positions"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["quantity"] != json.Number("1") || rows[0].(map[string]any)["reserve"] != json.Number("1") {
			t.Fatal("root or child retained whole original allocation")
		}
		total++
	}
	if total != wanted {
		t.Fatal("native root plus child quantity did not conserve original split")
	}
}

func TestOrdersRetainedAdmissionRejectsFreshParentOrForeignNativeDrift(t *testing.T) {
	cases := []struct {
		name   string
		change func(positionHTTPFixture, *unmirroredSplitHTTPProvider)
	}{
		{"source allocation", func(f positionHTTPFixture, p *unmirroredSplitHTTPProvider) {
			f.provider.base.server.Config.Handler = changedAdmissionRoot(p)
		}},
		{"foreign reserve", func(f positionHTTPFixture, _ *unmirroredSplitHTTPProvider) { f.addForeignLine(t) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, provider, admission := retainedAdmissionHTTPFixture(t)
			preview := splitMemberHTTPPreview(t, f, "split-http-child", nil)
			if preview.Decisions[0].Commands[0].SplitTransfer.ParentWorkID != admission.ID {
				t.Fatal("wrong native basis")
			}
			f.approve(t, preview)
			tc.change(f, provider)
			f.run(t, orders.StateBlocked)
			if f.latestWork(t).DispatchCount != 0 || provider.writes.Load() != 1 {
				t.Fatal("stale admission dispatched or replayed split")
			}
			f.provider.base.mu.Lock()
			defer f.provider.base.mu.Unlock()
			if len(f.provider.base.native) != 1 {
				t.Fatal("drift created child native order")
			}
			for _, doc := range f.provider.base.native {
				rows := doc.body["positions"].([]any)
				if len(rows) > 1 && rows[1].(map[string]any)["reserve"] != json.Number("2") {
					t.Fatal("foreign reserve altered")
				}
			}
		})
	}
}

func changedAdmissionRoot(p *unmirroredSplitHTTPProvider) http.Handler {
	original := retainedUnassembledHTTPProvider{p}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/posting/fbs/get" {
			original.serve(w, r)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			w.WriteHeader(400)
			return
		}
		var query struct {
			Posting string `json:"posting_number"`
		}
		if json.Unmarshal(raw, &query) != nil {
			w.WriteHeader(400)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		response := httptest.NewRecorder()
		original.serve(response, r)
		if response.Code != 200 {
			w.WriteHeader(response.Code)
			return
		}
		var payload map[string]map[string]any
		if json.Unmarshal(response.Body.Bytes(), &payload) != nil {
			w.WriteHeader(500)
			return
		}
		if query.Posting == "position-http-posting" {
			payload["result"]["products"].([]any)[0].(map[string]any)["quantity"] = 2
			payload["result"]["updated_at"] = "2026-11-04T12:00:02Z"
		}
		p.base.base.reply(w, payload)
	})
}

func TestOrdersRetainedAdmissionUnknownCreateNeverUnlocksOrResends(t *testing.T) {
	f, provider, split := retainedUnmirroredUnassembled(t)
	f.provider.base.mu.Lock()
	f.provider.base.lost[split.Command.PostingNumber] = true
	f.provider.base.mu.Unlock()
	f.approve(t, splitMemberHTTPPreview(t, f, split.Command.PostingNumber, nil))
	f.run(t, orders.StateUnknown)
	if next := unmirroredUnassembledHTTPReview(t, f, "split-http-child"); next.Decisions[0].Ready {
		t.Fatal("unknown root creation granted child admission")
	}
	if provider.writes.Load() != 1 {
		t.Fatal("UNKNOWN root replayed Ozon split")
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 1 {
		t.Fatal("lost acknowledgment duplicated native creation")
	}
}

func TestOrdersRetainedAdmissionDoesNotAdoptPreSplitFullNativeReserve(t *testing.T) {
	f, provider, initial := unmirroredSplitFixture(t)
	f.provider.base.server.Config.Handler = http.HandlerFunc(retainedUnassembledHTTPProvider{provider}.serve)
	f.approve(t, f.preview(t)) // The native root really exists with the ORIGINAL two units.
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	unit := orders.ActionQuantity{Identity: initial.Lines[0].Identity, Quantity: "1"}
	proposal := orders.ActionProposal{PostingNumber: initial.PostingNumber, Action: orders.ActionPostingSplitUnassembled, Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{unit}}, {Lines: []orders.ActionQuantity{unit}}}}}
	f.approve(t, splitMemberHTTPPreview(t, f, initial.PostingNumber, []orders.ActionProposal{proposal}))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	child := unmirroredUnassembledHTTPReview(t, f, "split-http-child")
	if child.Decisions[0].Ready || len(child.Decisions[0].Commands) != 0 {
		t.Fatal("pre-split full parent native reserve was admitted as reduced creation")
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 1 {
		t.Fatal("blocked child created a duplicate native order")
	}
	for _, native := range f.provider.base.native {
		line := native.body["positions"].([]any)[0].(map[string]any)
		if line["quantity"] != json.Number("2") || line["reserve"] != json.Number("2") {
			t.Fatal("proof silently released or changed original native reserve")
		}
	}
	if provider.writes.Load() != 1 {
		t.Fatal("split was repeated")
	}
}

func completeRetainedAdmissionChild(t *testing.T, f positionHTTPFixture) {
	t.Helper()
	admission := f.latestWork(t)
	child := unmirroredUnassembledHTTPReview(t, f, "split-http-child")
	if !child.Decisions[0].Ready || len(child.Decisions[0].Commands) != 1 || child.Decisions[0].Commands[0].SplitTransfer == nil || child.Decisions[0].Commands[0].SplitTransfer.ParentWorkID != admission.ID {
		t.Fatal("reconciled retained root did not establish exact typed admission")
	}
	f.approve(t, child)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertRetainedAdmissionNativeTotal(t, f, 2)

}
