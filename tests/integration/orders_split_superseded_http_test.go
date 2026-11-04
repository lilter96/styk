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

// Unlike the retained fixture, this supported response replaces the old number
// with two new numbers. Parent retirement is independently observed via GET200.
type supersededSplitHTTPProvider struct{ *unmirroredSplitHTTPProvider }

func (p supersededSplitHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v3/posting/fbs/get" {
		p.replacementPosting(w, r)
		return
	}
	if r.URL.Path != "/v4/posting/fbs/ship" {
		p.unmirroredSplitHTTPProvider.serve(w, r)
		return
	}
	recorded := httptest.NewRecorder()
	p.unmirroredSplitHTTPProvider.serve(recorded, r)
	if recorded.Code != http.StatusOK {
		w.WriteHeader(recorded.Code)
		return
	}
	p.base.base.reply(w, map[string]any{"result": []string{"split-http-first", "split-http-child"}})
}

func (p supersededSplitHTTPProvider) replacementPosting(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body struct {
		Posting string `json:"posting_number"`
	}
	if json.Unmarshal(raw, &body) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	mapped := body.Posting
	if mapped == "split-http-first" {
		mapped = "split-http-child"
	}
	r.Body = io.NopCloser(bytes.NewBufferString(`{"posting_number":"` + mapped + `"}`))
	recorded := httptest.NewRecorder()
	p.posting(recorded, r)
	if recorded.Code != http.StatusOK {
		w.WriteHeader(recorded.Code)
		return
	}
	var payload map[string]map[string]any
	if json.Unmarshal(recorded.Body.Bytes(), &payload) != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	row := payload["result"]
	row["posting_number"] = body.Posting
	if p.split.Load() && body.Posting == "position-http-posting" {
		row["status"] = "cancelled_from_split_pending"
		row["products"].([]any)[0].(map[string]any)["quantity"] = json.Number("2")
	}
	p.base.base.reply(w, payload)
}

func supersededSplitHTTPFixture(t *testing.T) (positionHTTPFixture, *unmirroredSplitHTTPProvider, orders.Work) {
	t.Helper()
	f, provider, initial := unmirroredSplitFixture(t)
	f.provider.base.server.Config.Handler = http.HandlerFunc(supersededSplitHTTPProvider{provider}.serve)
	f.approve(t, splitMemberHTTPPreview(t, f, initial.PostingNumber, nil))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.addForeignLine(t)
	unit := orders.ActionQuantity{Identity: initial.Lines[0].Identity, Quantity: "1"}
	proposal := orders.ActionProposal{PostingNumber: initial.PostingNumber, Action: orders.ActionPostingSplit, Parameters: orders.ActionParameters{ReadinessConfirmed: true, Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{unit}}, {Lines: []orders.ActionQuantity{unit}}}}}
	f.approve(t, splitMemberHTTPPreview(t, f, initial.PostingNumber, []orders.ActionProposal{proposal}))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	return f, provider, f.latestWork(t)
}

func TestOrdersSupersededSplitReleasesOnlyOwnedParentReserve(t *testing.T) {
	f, provider, split := supersededSplitHTTPFixture(t)
	preview := f.preview(t) // actual detail GET200, normal durable source observation
	if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("retired parent not reviewable: %+v", preview.Decisions[0].Validation)
	}
	command := preview.Decisions[0].Commands[0]
	if command.Action != orders.ActionMirrorPositionUpdate || command.SupersededSplitWorkID != split.ID || command.PositionPatch == nil || command.PositionPatch.Quantity.IsSpecified() || command.PositionPatch.Price.IsSpecified() || !command.PositionPatch.Reserve.IsKnown() || *command.PositionPatch.Reserve.Value != "0" {
		t.Fatal("retirement did not produce a separate exact owned reserve-only intent")
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertReservePolicyNative(t, f, "0")
	target := f.readNative(t)
	if !target.Fields.Applicable.IsKnown() || !*target.Fields.Applicable.Value || target.Positions[0].Quantity != "2" {
		t.Fatal("reserve retirement changed document application or source quantity")
	}
	if provider.writes.Load() != 1 || f.provider.patches.Load() != 1 {
		t.Fatal("split or reserve mutation was replayed")
	}
	if next := f.preview(t); !next.Decisions[0].Ready || len(next.Decisions[0].Commands) != 0 {
		t.Fatal("repeated retirement generated an unnecessary write", next.Decisions[0].Validation)
	}
	completeSupersededSplitChildren(t, f, split, target.Correlation.CustomerOrderID)
	if provider.writes.Load() != 1 || f.provider.patches.Load() != 1 {
		t.Fatal("child transfer replayed split or original reserve retirement")
	}
}

func completeSupersededSplitChildren(t *testing.T, f positionHTTPFixture, split orders.Work, parentID string) {
	t.Helper()
	for _, number := range split.Receipt.PostingNumbers {
		child := splitMemberHTTPPreview(t, f, number, nil)
		proof := child.Decisions[0].Commands[0].SplitTransfer
		if proof == nil || proof.SplitWorkID != split.ID || proof.ParentCorrelation.CustomerOrderID != parentID {
			t.Fatal("replacement child did not retain acknowledged parent retirement")
		}
		f.approve(t, child)
		f.run(t, orders.StateAcknowledged)
		f.run(t, orders.StateReconciled)
		native := f.readNative(t)
		if len(native.Positions) != 1 || !native.Positions[0].Reserve.IsKnown() || *native.Positions[0].Reserve.Value != "1" {
			t.Fatal("replacement child did not independently reserve its own unit")
		}
		if next := splitMemberHTTPPreview(t, f, number, nil); len(next.Decisions[0].Commands) != 0 {
			t.Fatal("unchanged replacement child generated a second external write")
		}
	}
}
