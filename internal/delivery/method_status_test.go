package delivery

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestMethodStatusIsIndependentEvidence(t *testing.T) {
	t.Parallel()
	for _, status := range []orders.Field[string]{{}, {Presence: orders.PresenceNull}, orders.Present("ACTIVE"), orders.Present("DISABLED"), orders.Present("FUTURE_STATE")} {
		t.Run(string(status.Presence)+known(status), func(t *testing.T) {
			t.Parallel()
			warehouse := Warehouse{ID: "warehouse", Type: orders.Present("rfbs"), Status: orders.Present("created")}
			method := Method{ID: "method", WarehouseID: warehouse.ID, Status: status}
			profile, err := Classify(Scope{"tenant", "account"}, warehouse, &method, testEvidence(t), time.Now().UTC())
			if err != nil || profile.MethodStatus.Presence != status.Presence || known(profile.MethodStatus) != known(status) {
				t.Fatalf("method status changed: %+v %v", profile, err)
			}
			if slices.Contains(profile.VerifiedDimensions, "method_status") != status.IsKnown() {
				t.Fatal("unknown method status became verified")
			}
			if profile.CapabilityRef() == nil {
				t.Fatal("lifecycle state invented a universal prohibition on existing posting actions")
			}
			if known(status) == "FUTURE_STATE" && !slices.ContainsFunc(profile.Drift, func(d orders.Drift) bool { return d.Code == "unknown_enum" }) {
				t.Fatal("new provider status was silently accepted")
			}
		})
	}
}

func TestLegacyDeliveryProfileIdentityDoesNotAcquireMissingStatus(t *testing.T) {
	t.Parallel()
	profile, err := Classify(Scope{"tenant", "account"}, Warehouse{ID: "warehouse"}, nil, testEvidence(t), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(profile)
	if err != nil || strings.Contains(string(raw), "method_status") {
		t.Fatal("new absent field changed legacy profile identity")
	}
	var restored Profile
	if err = json.Unmarshal(raw, &restored); err != nil || restored.Validate() != nil || restored.ID != profile.ID {
		t.Fatal("legacy profile no longer validates")
	}
}
