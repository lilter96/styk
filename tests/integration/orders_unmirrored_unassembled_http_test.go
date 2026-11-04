//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func completedUnmirroredUnassembled(t *testing.T) (positionHTTPFixture, *unassembledSplitHTTPProvider, orders.Work) {
	t.Helper()
	f, provider, preview := unassembledHTTPFixture(t)
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StatePolling)
	f.run(t, orders.StateReconciled)
	return f, provider, f.latestWork(t)
}

func TestOrdersUnmirroredUnassembledSupersededRootMirrorsOnlyConfirmedChildren(t *testing.T) {
	f, provider, split := completedUnmirroredUnassembled(t)
	assertUnmirroredSplitRejectsForeignScope(t, f, split.Receipt.PostingNumbers[0])
	for _, number := range split.Receipt.PostingNumbers[:2] {
		preview := splitMemberHTTPPreview(t, f, number, nil)
		command := preview.Decisions[0].Commands[0]
		if command.Action != orders.ActionMirrorCreate || command.SplitSourceWorkID != split.ID || command.CustomerOrder.Positions[0].Quantity != "1" || !command.CustomerOrder.Positions[0].Reserve.IsKnown() || *command.CustomerOrder.Positions[0].Reserve.Value != "1" {
			t.Fatal("child intent lost exact scoped allocation authority")
		}
		f.approve(t, preview)
		f.run(t, orders.StateAcknowledged)
		f.run(t, orders.StateReconciled)
		native := f.readNative(t)
		if len(native.Positions) != 1 || native.Positions[0].Quantity != "1" || !native.Positions[0].Reserve.IsKnown() || *native.Positions[0].Reserve.Value != "1" {
			t.Fatal("normal native readback did not reserve child exactly once")
		}
		if next := splitMemberHTTPPreview(t, f, number, nil); len(next.Decisions[0].Commands) != 0 {
			t.Fatal("unchanged child recreated native document")
		}
	}
	if provider.writes.Load() != 1 {
		t.Fatal("unassembled split was resent")
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 2 {
		t.Fatal("unmirrored whole parent or duplicate native order was created")
	}
}

func unmirroredUnassembledHTTPReview(t *testing.T, f positionHTTPFixture, number string) orders.Preview {
	t.Helper()
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{number}})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != 200 || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatalf("merchant review failed status%d", status)
	}
	return preview
}

func assertUnmirroredSplitRejectsForeignScope(t *testing.T, f positionHTTPFixture, number string) {
	t.Helper()
	for _, foreign := range []map[string]any{
		{"tenant": f.config.Scope.Tenant, "moysklad_account": "other-moy", "ozon_account": f.config.Scope.OzonAccount},
		{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": "other-ozon"},
	} {
		foreign["expected_revision"], foreign["posting_numbers"] = f.config.Revision, []string{number}
		raw, err := json.Marshal(foreign)
		if err != nil {
			t.Fatal(err)
		}
		status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(raw), true)
		if status != 403 && status != 404 {
			t.Fatalf("cross-account proof request returned status%d", status)
		}
	}
}
