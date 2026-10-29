package orderactions

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

func fbsFixture(t *testing.T) (orders.Posting, orders.Command, time.Time) {
	t.Helper()
	now := time.Now().UTC()
	scope := orders.Scope{Tenant: "fixture-tenant", OzonAccount: "fixture-ozon", MoyskladAccount: "fixture-moy"}
	capability := orders.CapabilityRef{ID: "reviewed-profile", Revision: 1, WarehouseID: "900719925474099312345", DeliveryMethodID: "900719925474099312346",
		Scheme: "FBS", Carrier: orders.Present("ozon"), Destination: orders.Present("customer"), Express: orders.Present(false),
		VerifiedDimensions: []string{"scheme", "warehouse", "carrier", "express", "destination"}, ObservedAt: now.Add(-time.Minute), ValidUntil: now.Add(time.Minute)}
	posting := orders.Posting{Scope: scope, CommercialOrderID: "commercial-fixture", PostingNumber: "TEST-FBS-0001-1",
		Scheme: orders.Present("FBS"), Status: orders.Present("awaiting_packaging"), WarehouseID: orders.Present(capability.WarehouseID),
		DeliveryMethodID: orders.Present(capability.DeliveryMethodID), DeliveryProfile: &capability,
		Evidence: orders.EvidenceRevision{Revision: "source-1", Origin: orders.OriginOzon, ObservedAt: now},
		Lines:    []orders.PostingLine{{Identity: orders.LineIdentity{SKU: "900719925474099312347", OfferID: "fixture-offer"}, Quantity: "3", CancelledQuantity: orders.Present(orders.Decimal("0"))}}}
	correlation, err := orders.CorrelationFor(scope, posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	command := orders.Command{ID: "fixture-command", Scope: scope, Action: orders.ActionPostingPack, CommercialOrderID: posting.CommercialOrderID,
		PostingNumber: posting.PostingNumber, Correlation: correlation, ConfigRevision: 1, Credentials: orders.CredentialRevisions{Ozon: 1, Moysklad: 1},
		ExpectedSourceRevision: posting.Evidence.Revision, SourceOrigin: orders.OriginUser, IdempotencyKey: "fixture-intent-1", CreatedAt: now,
		Warehouse: orders.WarehouseMappingRef{ID: "warehouse-map", Version: 1, Scope: scope, OzonWarehouseID: capability.WarehouseID, MoyskladStoreID: "fixture-store",
			Confirmed: true, ConfirmedAt: now, Capability: capability}, PostingAction: &orders.PostingAction{Capability: capability}}
	return posting, command, now
}

func TestPlanFBSNeverDispatchesUnverifiedSchema(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		action orders.Action
		path   string
	}{
		{name: "pack", action: orders.ActionPostingPack, path: FBSShipEndpoint},
		{name: "physical ship", action: orders.ActionPostingShip},
		{name: "cancel", action: orders.ActionPostingCancel},
		{name: "unsupported delivery", action: orders.ActionPostingDeliver},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting, command, _ := fbsFixture(t)
			command.Action = tc.action
			plan, err := PlanFBS(posting, command)
			if !errors.Is(err, orders.ErrBlocked) || plan.Allowed || len(plan.Body) != 0 || plan.Endpoint != tc.path {
				t.Fatalf("unverified action escaped: plan=%+v err=%v", plan, err)
			}
			if plan.PostingNumber != posting.PostingNumber || plan.SourceRevision != posting.Evidence.Revision || len(plan.Diagnostics) == 0 {
				t.Fatalf("missing review evidence: %+v", plan)
			}
		})
	}
}

func TestPlanFBSRejectsStaleScopeAndSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*orders.Posting, *orders.Command)
		want   error
	}{
		{name: "source", change: func(p *orders.Posting, _ *orders.Command) { p.Evidence.Revision = "source-2" }, want: orders.ErrStale},
		{name: "posting", change: func(p *orders.Posting, _ *orders.Command) { p.PostingNumber = "other-posting" }, want: orders.ErrStale},
		{name: "scope", change: func(p *orders.Posting, _ *orders.Command) { p.Scope.Tenant = "other-tenant" }, want: orders.ErrStale},
		{name: "unknown action", change: func(_ *orders.Posting, c *orders.Command) { c.Action = "future_action" }, want: orders.ErrInvalid},
		{name: "STYK origin", change: func(_ *orders.Posting, c *orders.Command) { c.SourceOrigin = orders.OriginSTYK }, want: orders.ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting, command, _ := fbsFixture(t)
			tc.change(&posting, &command)
			plan, err := PlanFBS(posting, command)
			if !errors.Is(err, tc.want) || plan.Allowed || len(plan.Body) != 0 {
				t.Fatalf("stale command accepted: plan=%+v err=%v", plan, err)
			}
		})
	}
}

func TestFBSExcludedAndUnknownStatusDiagnostics(t *testing.T) {
	t.Parallel()
	posting, command, _ := fbsFixture(t)
	posting.Scheme = orders.Present("FBO")
	plan, err := PlanFBS(posting, command)
	if !errors.Is(err, orders.ErrBlocked) || !fbsHasDiagnostic(plan.Diagnostics, "error", "fbo_excluded") {
		t.Fatalf("FBO not excluded: %+v %v", plan, err)
	}
	posting.Scheme, posting.Status = orders.Present("FBS"), orders.Present("future_state")
	plan, err = PlanFBS(posting, command)
	if !errors.Is(err, orders.ErrBlocked) || !fbsHasDiagnostic(plan.Diagnostics, "error", "unknown_fbs_status") {
		t.Fatalf("unknown status not blocked: %+v %v", plan, err)
	}
}

func TestFBSProfileRequiresReviewedDimensions(t *testing.T) {
	t.Parallel()
	posting, command, now := fbsFixture(t)
	capability := command.PostingAction.Capability
	capability.VerifiedDimensions = []string{"scheme", "warehouse"}
	capability.ValidUntil = now
	diagnostics := ValidateFBSProfile(posting, capability, now)
	for _, code := range []string{"unverified_carrier", "unverified_express", "unverified_destination", "reviewed_profile_expired", "provider_permissions_unverified"} {
		if !fbsHasDiagnostic(diagnostics, "error", code) {
			t.Errorf("missing diagnostic %s: %+v", code, diagnostics)
		}
	}
	if !fbsHasDiagnostic(diagnostics, "warning", "available_actions_absent") {
		t.Fatalf("optional absence became an error: %+v", diagnostics)
	}
	posting.AvailableActions = []string{"future_action"}
	if !fbsHasDiagnostic(ValidateFBSProfile(posting, capability, now), "error", "available_action_enum_unverified") {
		t.Fatal("unverified available_action enum accepted")
	}
}

func TestFBSGraphKeepsCurrentLabelRoutesAndBlocksSchemas(t *testing.T) {
	t.Parallel()
	seen := make(map[FBSOperation]bool)
	for _, node := range FBSGraph() {
		if seen[node.Operation] || node.SchemaVerified || len(node.Prerequisites) == 0 || len(node.Sources) == 0 {
			t.Fatalf("invalid graph evidence: %+v", node)
		}
		seen[node.Operation] = true
	}
	create, _ := fbsNode(FBSLabelCreate)
	get, _ := fbsNode(FBSLabelGet)
	if create.Endpoint != FBSLabelCreateEndpoint || get.Endpoint != FBSLabelGetEndpoint || len(seen) != 9 {
		t.Fatalf("missing current FBS graph: %+v", FBSGraph())
	}
	if !fbsHasDiagnostic(AssessFBSOperation("invented"), "error", "unknown_fbs_operation") {
		t.Fatal("unknown graph operation accepted")
	}
}

func TestFBSLabelPlaceholderIsNotVerifiedContract(t *testing.T) {
	t.Parallel()
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{FBSLabelCreateEndpoint, FBSLabelGetEndpoint} {
		report := manifest.Validate(endpoint, "request", []byte(`{}`))
		if report.SchemaVerified {
			t.Fatalf("placeholder represented as official schema: %s", endpoint)
		}
	}
}

func fbsHasDiagnostic(diagnostics []Diagnostic, severity, code string) bool {
	return slices.ContainsFunc(diagnostics, func(item Diagnostic) bool { return item.Severity == severity && item.Code == code })
}
