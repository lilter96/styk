package orderflow

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestActionOptionsDoNotAdvertiseUnimplementedDispatch(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	review.Postings[0].Lines[0].Identity.SKU = "123"
	review.WarehouseMappings[0].Capability = orders.CapabilityRef{ID: "cap", Revision: 1, WarehouseID: "warehouse", DeliveryMethodID: "delivery-method", Scheme: "FBS", VerifiedDimensions: []string{"scheme", "warehouse"}, ObservedAt: flowTestNow.Add(-time.Minute), ValidUntil: flowTestNow.Add(time.Hour)}
	store := &flowStore{review: review}
	service := flowService(t, store, flowResolver{source: &flowProvider{}, target: &flowProvider{}})
	options, err := service.ActionOptions(context.Background(), orders.ActionOptionsRequest{Scope: review.Config.Scope, PostingNumber: "posting"})
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Actions) == 0 {
		t.Fatal("missing action options")
	}
	for _, option := range options.Actions {
		// Cancel actions require provider reason listing; tracking/ship/last-mile/deliver require rFBS.
		if option.Action == orders.ActionPostingCancel || option.Action == orders.ActionPostingPartialCancel || option.Action == orders.ActionPostingTracking || option.Action == orders.ActionPostingShip || option.Action == orders.ActionPostingLastMile || option.Action == orders.ActionPostingDeliver || option.Action == orders.ActionPostingLabelCreate || option.Action == orders.ActionPostingSentBySeller {
			continue
		}
		if !option.Allowed || len(option.Blockers) > 0 {
			t.Fatalf("reviewed action should be executable: %+v", option)
		}
	}
}

func TestCustomerMappingWithoutProtectorBlocks(t *testing.T) {
	t.Parallel()
	validation := applyCustomerMapping(&orders.CustomerOrderWrite{}, orders.Posting{}, orders.FieldMapping{Source: "customer.address"}, nil)
	if validation.Code != "protected_customer_projection_unavailable" {
		t.Fatalf("unavailable PII mapping silently accepted: %+v", validation)
	}
}

func TestActionProposalPreviewMatchesConsoleRequestShape(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	review.Postings[0].Lines[0].Identity.SKU = "123"
	review.Postings[0].Status = orders.Present("awaiting_packaging")
	review.WarehouseMappings[0].Capability = orders.CapabilityRef{ID: "cap", Revision: 1, WarehouseID: "warehouse", DeliveryMethodID: "delivery-method", Scheme: "FBS", VerifiedDimensions: []string{"scheme", "warehouse"}, ObservedAt: flowTestNow.Add(-time.Minute), ValidUntil: flowTestNow.Add(time.Hour)}
	store := &flowStore{review: review}
	service := flowService(t, store, flowResolver{source: &flowProvider{}, target: &flowProvider{}})
	proposal := orders.ActionProposal{PostingNumber: "posting", Action: orders.ActionPostingPack,
		Parameters: orders.ActionParameters{ReadinessConfirmed: true, Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{{Identity: orders.LineIdentity{SKU: "123", OfferID: "offer"}, Quantity: "2"}}}}}}
	preview, err := service.Preview(context.Background(), orders.PreviewRequest{Scope: review.Config.Scope, ExpectedRevision: review.Config.Revision,
		PostingNumbers: []string{"posting"}, Actions: []orders.Action{orders.ActionPostingPack}, Proposals: []orders.ActionProposal{proposal}, Actor: "authenticated-owner"})
	if err != nil {
		t.Fatalf("console proposal shape rejected: %v", err)
	}
	if len(preview.Decisions) != 1 || len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("reviewed action should produce one command: %+v", preview.Decisions)
	}
	if !slices.ContainsFunc(preview.Decisions[0].Validation, func(v orders.Validation) bool {
		return v.Code == "" || v.Code == "protected_customer_projection_unavailable"
	}) && len(preview.Decisions[0].Validation) > 0 {
		t.Fatalf("unexpected validation blockers: %+v", preview.Decisions[0].Validation)
	}
}

func TestActionProposalPreviewRejectsExtraTargets(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	store := &flowStore{review: review}
	service := flowService(t, store, flowResolver{source: &flowProvider{}, target: &flowProvider{}})
	_, err := service.Preview(context.Background(), orders.PreviewRequest{Scope: review.Config.Scope, ExpectedRevision: review.Config.Revision,
		PostingNumbers: []string{"posting", "other"}, Actions: []orders.Action{orders.ActionPostingCancel},
		Proposals: []orders.ActionProposal{{PostingNumber: "posting", Action: orders.ActionPostingCancel}}, Actor: "authenticated-owner"})
	if err != orders.ErrInvalid {
		t.Fatalf("mismatched proposal coverage accepted: %v", err)
	}
}
