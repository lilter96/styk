//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersSplitRetainedParentReleasesOnlyItsOwnedReserve(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "acknowledged", true: "lost_parent_response"}[lost], func(t *testing.T) {
			f, provider, initial := unmirroredSplitFixture(t)
			f.approve(t, splitMemberHTTPPreview(t, f, initial.PostingNumber, nil))
			f.run(t, orders.StateAcknowledged)
			f.run(t, orders.StateReconciled)
			f.addForeignLine(t)
			unit := orders.ActionQuantity{Identity: initial.Lines[0].Identity, Quantity: "1"}
			proposal := orders.ActionProposal{PostingNumber: initial.PostingNumber, Action: orders.ActionPostingSplit, Parameters: orders.ActionParameters{ReadinessConfirmed: true, Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{unit}}, {Lines: []orders.ActionQuantity{unit}}}}}
			f.approve(t, splitMemberHTTPPreview(t, f, initial.PostingNumber, []orders.ActionProposal{proposal}))
			f.run(t, orders.StateAcknowledged)
			f.run(t, orders.StateReconciled)
			split := f.latestWork(t)
			assertSplitChildReserveHeld(t, f)
			reduction := splitMemberHTTPPreview(t, f, initial.PostingNumber, nil)
			command := reduction.Decisions[0].Commands[0]
			if command.RetainedSplitWorkID != split.ID || command.Action != orders.ActionMirrorPositionUpdate || command.PositionPatch == nil || !command.PositionPatch.Quantity.IsKnown() || *command.PositionPatch.Quantity.Value != "1" || !command.PositionPatch.Reserve.IsKnown() || *command.PositionPatch.Reserve.Value != "1" {
				t.Fatal("retained package did not produce a sealed owned reduction")
			}
			f.approve(t, reduction)
			f.provider.losePatchResponse.Store(lost)
			state := orders.StateAcknowledged
			if lost {
				state = orders.StateUnknown
			}
			f.run(t, state)
			assertSplitChildReserveHeld(t, f)
			f.run(t, orders.StateReconciled)
			assertReservePolicyNative(t, f, "1")
			assertSplitChildReserveHeld(t, f)
			if provider.writes.Load() != 1 || f.provider.patches.Load() != 1 {
				t.Fatal("split or uncertain native reduction was replayed")
			}
			if next := splitMemberHTTPPreview(t, f, initial.PostingNumber, nil); len(next.Decisions[0].Commands) != 0 {
				t.Fatal("unchanged retained package generated another native write")
			}
		})
	}
}

func assertSplitChildReserveHeld(t *testing.T, f positionHTTPFixture) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{"split-http-child"}})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != 200 || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatalf("split child review failed: HTTP %d", status)
	}
	decision := preview.Decisions[0]
	if decision.Ready || len(decision.Commands) != 0 || !slices.ContainsFunc(decision.Validation, func(v orders.Validation) bool { return v.Code == "split_reserve_transfer_pending" }) {
		t.Fatal("child reserve was unlocked without a separate transfer proof", decision.Validation)
	}
}
