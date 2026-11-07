package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestValidateOrderNativeStateBaselineFencesLegacyAndForeignCommands(t *testing.T) {
	t.Parallel()
	scope := orders.Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}
	correlation, err := orders.CorrelationFor(scope, "posting")
	if err != nil {
		t.Fatal(err)
	}
	correlation.CustomerOrderID = "native-order"
	baseline := orders.NativeStateBaseline{Scope: scope, Correlation: correlation, WorkID: "work", CommandID: "command", StateID: "own-state", TargetRevision: "revision", ObservedAt: time.Now().UTC()}
	current := orders.ReviewContext{NativeStateBaselines: map[string]orders.NativeStateBaseline{"posting": baseline}}
	tests := []struct {
		name   string
		change func(*orders.Command)
		allow  bool
	}{
		{"exact proof", func(*orders.Command) {}, true},
		{"legacy queued state update", func(c *orders.Command) { c.NativeStateBaseline = nil }, false},
		{"changed target revision", func(c *orders.Command) { c.ExpectedTargetRevision = "new-revision" }, false},
		{"foreign order", func(c *orders.Command) { c.Correlation.CustomerOrderID = "foreign-order" }, false},
		{"forged work", func(c *orders.Command) { c.NativeStateBaseline.WorkID = "forged-work" }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proof := baseline
			command := orders.Command{Action: orders.ActionMirrorUpdate, PostingNumber: "posting", Correlation: correlation, ExpectedTargetRevision: "revision", NativeStateBaseline: &proof, CustomerOrder: &orders.CustomerOrderWrite{StateID: "next-state", OwnedFields: []string{"state"}}}
			tc.change(&command)
			err := validateOrderNativeStateBaseline(command, current)
			if tc.allow && err != nil || !tc.allow && !errors.Is(err, orders.ErrStale) {
				t.Fatalf("state provenance guard: allowed=%v err=%v", tc.allow, err)
			}
		})
	}
}

func TestPreservedMerchantStateDoesNotAuthorizeWritingIt(t *testing.T) {
	t.Parallel()
	current := orders.ReviewContext{Config: orders.Config{OrganizationID: "organization", AgentID: "agent", SalesChannelID: "channel", StateID: "configured-state"}}
	for _, tc := range []struct {
		name   string
		action orders.Action
		fields []string
		allow  bool
	}{
		{"preserved merchant state", orders.ActionMirrorUpdate, []string{"name"}, true},
		{"unauthorized state write", orders.ActionMirrorUpdate, []string{"state"}, false},
		{"new order state", orders.ActionMirrorCreate, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command := orders.Command{Action: tc.action, CustomerOrder: &orders.CustomerOrderWrite{OrganizationID: "organization", AgentID: "agent", SalesChannelID: "channel", StateID: "merchant-state", OwnedFields: tc.fields}}
			err := validateOrderNativeWrite(command, current)
			if (err == nil) != tc.allow {
				t.Fatalf("preserved native state changed write authorization: %v", err)
			}
		})
	}
}
