package orderflow

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func automaticNativeReview(t *testing.T) (orders.ReviewContext, orders.MerchantIntent) {
	t.Helper()
	review := flowReview(t, "100-1")
	before, after := "00000000-0000-4000-8000-000000000021", "00000000-0000-4000-8000-000000000022"
	correlation := review.Correlations[0]
	correlation.CustomerOrderID = "00000000-0000-4000-8000-000000000023"
	review.Correlations[0] = correlation
	intent := orders.MerchantIntent{ID: "00000000-0000-4000-8000-000000000024", Scope: review.Config.Scope, Correlation: correlation, BeforeStateID: before, AfterStateID: after, BeforeRevision: "before", AfterRevision: "after", BeforeUpdatedAt: "2026-11-06 10:00:00", AfterUpdatedAt: "2026-11-06 10:01:00", ObservedAt: time.Date(2026, 11, 6, 7, 1, 1, 0, time.UTC), ConfigRevision: review.Config.Revision, CredentialRevision: review.Credentials.Moysklad, Action: orders.ActionDemandCreate, Scheme: "FBS", EventKind: "handoff_confirmed", WholePosting: true}
	policy := &orders.LifecyclePolicy{ID: "00000000-0000-4000-8000-000000000025", Scope: review.Config.Scope, Revision: 1, ConfigRevision: review.Config.Revision, OwnerID: "owner", Enabled: true, GrantedAt: intent.ObservedAt.Add(-time.Hour), AutoDemand: true, DemandMoment: orders.OnHandoffConfirmed, Mappings: []orders.MerchantCommandMapping{{StateID: after, FromStateIDs: []string{before}, Action: intent.Action, Scheme: intent.Scheme, EventKind: intent.EventKind, WholePosting: true}}}
	review.Config.Lifecycle = policy
	sealAutomaticNativeIntent(t, &intent)
	return review, intent
}

func sealAutomaticNativeIntent(t *testing.T, intent *orders.MerchantIntent) {
	t.Helper()
	var err error
	intent.Digest, err = intent.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticNativeProposalUsesOnlyConfiguredPhysicalTransition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*orders.ReviewContext, *orders.MerchantIntent)
		allow  bool
	}{
		{name: "whole posting handoff", allow: true},
		{name: "future unknown source status", change: func(r *orders.ReviewContext, _ *orders.MerchantIntent) {
			r.Postings[0].Status = orders.Present("future_ozon_status")
		}},
		{name: "awaiting deliver alone", change: func(r *orders.ReviewContext, i *orders.MerchantIntent) {
			r.Postings[0].Status = orders.Present("awaiting_deliver")
			i.BeforeStateID = i.AfterStateID
		}},
		{name: "not accepted is not acceptance", change: func(r *orders.ReviewContext, _ *orders.MerchantIntent) {
			r.Postings[0].Status = orders.Present("not_accepted")
		}},
		{name: "cancelled after event needs review", change: func(r *orders.ReviewContext, _ *orders.MerchantIntent) {
			r.Postings[0].Status = orders.Present("cancelled")
		}},
		{name: "partial cancellation needs review", change: func(r *orders.ReviewContext, _ *orders.MerchantIntent) {
			r.Postings[0].Lines[0].CancelledQuantity = orders.Present(orders.Decimal("1"))
		}},
		{name: "unknown cancellation quantity", change: func(r *orders.ReviewContext, _ *orders.MerchantIntent) {
			r.Postings[0].Lines[0].CancelledQuantity = orders.Field[orders.Decimal]{Presence: orders.PresenceNull}
		}},
		{name: "scheme changed", change: func(r *orders.ReviewContext, _ *orders.MerchantIntent) { r.Postings[0].Scheme = orders.Present("rFBS") }},
		{name: "missing scheme and no capability", change: func(r *orders.ReviewContext, _ *orders.MerchantIntent) { r.Postings[0].Scheme = orders.Field[string]{} }},
		{name: "missing scheme uses exact fresh capability", allow: true, change: func(r *orders.ReviewContext, i *orders.MerchantIntent) {
			r.Postings[0].Scheme = orders.Field[string]{}
			r.WarehouseMappings[0].Capability = orders.CapabilityRef{ID: "verified", Revision: 1, WarehouseID: "warehouse", DeliveryMethodID: "delivery-method", Scheme: "FBS", VerifiedDimensions: []string{"scheme", "warehouse"}, ObservedAt: i.ObservedAt.Add(-time.Minute), ValidUntil: i.ObservedAt.Add(time.Minute)}
		}},
		{name: "missing scheme cannot reuse another delivery method", change: func(r *orders.ReviewContext, i *orders.MerchantIntent) {
			r.Postings[0].Scheme = orders.Field[string]{}
			r.WarehouseMappings[0].Capability = orders.CapabilityRef{ID: "verified", Revision: 1, WarehouseID: "warehouse", DeliveryMethodID: "other-method", Scheme: "FBS", VerifiedDimensions: []string{"scheme", "warehouse"}, ObservedAt: i.ObservedAt.Add(-time.Minute), ValidUntil: i.ObservedAt.Add(time.Minute)}
		}},
		{name: "unsupported acceptance policy", change: func(r *orders.ReviewContext, _ *orders.MerchantIntent) {
			r.Config.Lifecycle.DemandMoment = orders.OnOzonAcceptanceConfirmed
		}},
		{name: "native order not correlated", change: func(_ *orders.ReviewContext, i *orders.MerchantIntent) { i.Correlation.CustomerOrderID = "" }},
		{name: "old credential transition", change: func(r *orders.ReviewContext, _ *orders.MerchantIntent) { r.Credentials.Moysklad++ }},
		{name: "unmapped physical state", change: func(_ *orders.ReviewContext, i *orders.MerchantIntent) {
			i.AfterStateID = "00000000-0000-4000-8000-000000000099"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			review, intent := automaticNativeReview(t)
			if tc.change != nil {
				tc.change(&review, &intent)
			}
			sealAutomaticNativeIntent(t, &intent)
			proposal, issue := AutomaticNativeProposal(review, review.Postings[0], intent)
			if (issue.Code == "") != tc.allow {
				t.Fatalf("automatic native proposal readiness = %v, want %v (%s)", issue.Code == "", tc.allow, issue.Code)
			}
			if tc.allow && (proposal.EventKind != "shipment_dispatched" || !proposal.EventConfirmed || len(proposal.Lines) != 1 || proposal.Lines[0].Quantity != review.Postings[0].Lines[0].Quantity || proposal.EventReference == "") {
				t.Fatal("physical declaration lost exact full-posting evidence")
			}
		})
	}
}

func TestAutomaticReturnRequiresUniqueAppliedDemand(t *testing.T) {
	t.Parallel()
	review, intent := automaticNativeReview(t)
	intent.Action, intent.EventKind = orders.ActionSalesReturnCreate, "return_received"
	review.Config.Lifecycle.AutoReturn = true
	review.Config.Lifecycle.Mappings[0].Action, review.Config.Lifecycle.Mappings[0].EventKind = intent.Action, intent.EventKind
	sealAutomaticNativeIntent(t, &intent)
	if _, issue := AutomaticNativeProposal(review, review.Postings[0], intent); issue.Code != "native_unique_applied_demand_required" {
		t.Fatal("return inferred without an applied demand", issue)
	}
	_, basis := nativeReturnBasisFixture()
	basis.Scope, basis.CustomerOrderID = intent.Scope, orders.Present(intent.Correlation.CustomerOrderID)
	review.NativeDocuments = map[string]orders.NativeDocumentBinding{"demand": {WorkID: "demand", PostingNumber: intent.Correlation.PostingNumber, Observation: basis}}
	proposal, issue := AutomaticNativeProposal(review, review.Postings[0], intent)
	if issue.Code != "" || proposal.DemandWorkID != "demand" || proposal.EventKind != "return_received" {
		t.Fatal("exact applied original demand was not retained", proposal, issue)
	}
	review.NativeDocuments["another"] = orders.NativeDocumentBinding{WorkID: "another", PostingNumber: intent.Correlation.PostingNumber, Observation: basis}
	if _, issue = AutomaticNativeProposal(review, review.Postings[0], intent); issue.Code != "native_unique_applied_demand_required" {
		t.Fatal("ambiguous demand adopted automatically", issue)
	}
}
