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

type mirroredUnassembledHTTPProvider struct {
	base     *unassembledSplitHTTPProvider
	retained bool
}

func (p mirroredUnassembledHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if !p.retained || (r.URL.Path != "/v1/posting/fbs/split" && r.URL.Path != "/v3/posting/fbs/get") {
		p.base.serve(w, r)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		w.WriteHeader(400)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	recorder := httptest.NewRecorder()
	p.base.serve(recorder, r)
	if recorder.Code != http.StatusOK {
		w.WriteHeader(recorder.Code)
		return
	}
	var payload map[string]any
	if json.Unmarshal(recorder.Body.Bytes(), &payload) != nil {
		w.WriteHeader(500)
		return
	}
	if r.URL.Path == "/v1/posting/fbs/split" {
		payload["postings"].([]any)[0].(map[string]any)["posting_number"] = "position-http-posting"
	} else if !p.adjustRetainedPosting(payload, raw) {
		w.WriteHeader(400)
		return
	}
	p.base.base.base.base.reply(w, payload)
}

func mirroredUnassembledHTTPFixture(t *testing.T, retained bool) (positionHTTPFixture, *unassembledSplitHTTPProvider, orders.Work) {
	t.Helper()
	f, provider, proposal := unassembledHTTPFixture(t)
	f.provider.base.server.Config.Handler = http.HandlerFunc(mirroredUnassembledHTTPProvider{base: provider, retained: retained}.serve)
	f.approve(t, f.preview(t))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.addForeignLine(t)
	original := proposal.Decisions[0].Commands[0]
	action := orders.ActionProposal{PostingNumber: original.PostingNumber, Action: original.Action, Parameters: original.PostingAction.Parameters}
	f.approve(t, splitMemberHTTPPreview(t, f, original.PostingNumber, []orders.ActionProposal{action}))
	f.run(t, orders.StateAcknowledged)
	if !retained {
		f.run(t, orders.StatePolling)
	}
	f.run(t, orders.StateReconciled)
	return f, provider, f.latestWork(t)
}

func TestOrdersUnassembledMirroredParentReleasesBeforeIndependentChildReserve(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "superseded", true: "retained"}[retained], func(t *testing.T) {
			f, provider, split := mirroredUnassembledHTTPFixture(t, retained)
			preview := f.preview(t)
			if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 {
				t.Fatal("mirrored parent reduction unavailable", preview.Decisions[0].Validation)
			}
			f.approve(t, preview)
			f.run(t, orders.StateAcknowledged)
			f.run(t, orders.StateReconciled)
			release := f.latestWork(t)
			reserve := "0"
			if retained {
				reserve = "1"
			}
			assertReservePolicyNative(t, f, reserve)
			childNumber := ordersProcessNumber("unassembled-child", 1)
			child := splitMemberHTTPPreview(t, f, childNumber, nil)
			if !child.Decisions[0].Ready || len(child.Decisions[0].Commands) != 1 {
				t.Fatal("child remained blocked after exact owned parent release", child.Decisions[0].Validation)
			}
			assertUnassembledTransferCommand(t, child.Decisions[0].Commands[0], split, release)
			f.approve(t, child)
			f.run(t, orders.StateAcknowledged)
			f.run(t, orders.StateReconciled)
			assertUnassembledFinalReserves(t, f, release, reserve)
			if provider.writes.Load() != 1 || provider.base.writes.Load() != 0 || f.provider.patches.Load() != 1 {
				t.Fatal("split or parent reserve was replayed")
			}
			if next := splitMemberHTTPPreview(t, f, childNumber, nil); !next.Decisions[0].Ready || len(next.Decisions[0].Commands) != 0 {
				t.Fatal("unchanged child generated another publication", next.Decisions[0].Validation)
			}
		})
	}
}

func assertUnassembledTransferCommand(t *testing.T, command orders.Command, split, release orders.Work) {
	t.Helper()
	proof := command.SplitTransfer
	if proof == nil || proof.SplitWorkID != split.ID || proof.ParentWorkID != release.ID || command.CustomerOrder == nil || len(command.CustomerOrder.Positions) != 1 {
		t.Fatal("new child lacks exact parent release proof")
	}
	reserve := command.CustomerOrder.Positions[0].Reserve
	if !reserve.IsKnown() || *reserve.Value != "1" {
		t.Fatal("child reserve does not equal its one independent unit")
	}
}

func assertUnassembledFinalReserves(t *testing.T, f positionHTTPFixture, release orders.Work, reserve string) {
	t.Helper()
	child := f.readNative(t)
	if !child.Complete || len(child.Positions) != 1 || !child.Positions[0].Reserve.IsKnown() || *child.Positions[0].Reserve.Value != "1" {
		t.Fatal("child native reserve was not independently observed")
	}
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "moysklad", f.config.Scope.MoyskladAccount, release.Credentials.Moysklad)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := binding.Client.(orders.TargetReader)
	if !ok {
		t.Fatal("parent native reader unavailable")
	}
	parent, err := reader.ReadCustomerOrder(f.ctx, release.Command.Correlation)
	if err != nil || !parent.Complete || len(parent.Positions) != 2 || !parent.Fields.Applicable.IsKnown() || !*parent.Fields.Applicable.Value {
		t.Fatal("complete conducted parent readback unavailable", err)
	}
	for _, position := range parent.Positions {
		expected := reserve
		if position.ID != ordersProcessID(999) {
			expected = "2"
		}
		if !position.Reserve.IsKnown() || string(*position.Reserve.Value) != expected {
			t.Fatal("child creation rewrote parent or foreign reserve")
		}
	}
}

func (p mirroredUnassembledHTTPProvider) adjustRetainedPosting(payload map[string]any, raw []byte) bool {
	var request struct {
		Posting string `json:"posting_number"`
	}
	if json.Unmarshal(raw, &request) != nil {
		return false
	}
	if p.base.base.split.Load() && request.Posting == "position-http-posting" {
		row := payload["result"].(map[string]any)
		row["status"] = "awaiting_packaging"
		row["products"].([]any)[0].(map[string]any)["quantity"] = 1
	}
	return true
}
