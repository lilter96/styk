package orderflow

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestServicePreviewIsolatesUnknownProductsAndKeepsSourceDriftInformational(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting-1")
	unknown := review.Postings[0]
	unknown.PostingNumber = "posting-2"
	unknown.Lines = slices.Clone(unknown.Lines)
	unknown.Lines[0].Identity.OfferID = "unmapped-offer"
	review.Postings[0].Drift = []orders.Drift{{Path: "$", Code: "schema_unverified"}}
	review.Postings = append(review.Postings, unknown)
	store := &flowStore{review: review}
	preview := flowPreview(t, flowService(t, store, flowResolver{}), review)
	if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 || len(preview.Decisions[0].Validation) != 0 {
		t.Fatalf("healthy posting was blocked by schema drift: %+v", preview.Decisions[0])
	}
	if preview.Decisions[1].Ready || len(preview.Decisions[1].Commands) != 0 || len(preview.Decisions[1].Validation) == 0 {
		t.Fatalf("unmapped posting was not isolated: %+v", preview.Decisions[1])
	}
	if preview.Decisions[0].Correlation.SyncID == preview.Decisions[1].Correlation.SyncID {
		t.Fatal("split postings share a customerorder identity")
	}
}

func TestServiceRequiresExplicitNonFBOSchemeEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		scheme     orders.Field[string]
		capability bool
		ready      bool
	}{{name: "missing", ready: false}, {name: "URL is not scheme", scheme: orders.Present("/v4/posting/fbs/list")},
		{name: "malformed scheme presence", scheme: orders.Field[string]{Presence: orders.PresenceValue}},
		{name: "FBO", scheme: orders.Present("FBO")}, {name: "explicit rFBS", scheme: orders.Present("rFBS"), ready: true},
		{name: "verified warehouse", capability: true, ready: true}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			review := flowReview(t, "posting")
			review.Postings[0].Scheme = test.scheme
			if test.capability {
				review.WarehouseMappings[0].Capability = flowCapability()
			}
			preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
			if preview.Decisions[0].Ready != test.ready {
				t.Fatalf("readiness=%v, want %v: %+v", preview.Decisions[0].Ready, test.ready, preview.Decisions[0].Validation)
			}
		})
	}
}

func flowCapability() orders.CapabilityRef {
	return orders.CapabilityRef{ID: "warehouse-evidence", Revision: 1, WarehouseID: "warehouse", DeliveryMethodID: "delivery-method", Scheme: "FBS",
		VerifiedDimensions: []string{"scheme", "warehouse"}, ObservedAt: flowTestNow.Add(-time.Minute), ValidUntil: flowTestNow.Add(time.Hour)}
}

func TestServiceHistoricalCancellationRequiresExplicitSafeFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		remove string
		ready  bool
	}{{name: "reviewed explicit policies", ready: true}, {name: "reserve omitted", remove: "positions.reserve"}, {name: "applicable omitted", remove: "applicable"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			review := flowReview(t, "posting")
			review.Postings[0].Status = orders.Present("cancelled")
			review.Config.FieldMappings = slices.DeleteFunc(review.Config.FieldMappings, func(mapping orders.FieldMapping) bool { return mapping.Target == test.remove })
			preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
			decision := preview.Decisions[0]
			if decision.Ready != test.ready {
				t.Fatalf("readiness=%v, want %v: %+v", decision.Ready, test.ready, decision.Validation)
			}
			if decision.Ready && (*decision.Commands[0].CustomerOrder.Applicable.Value || *decision.Commands[0].CustomerOrder.Positions[0].Reserve.Value != "0") {
				t.Fatal("historical cancellation would apply or reserve inventory")
			}
		})
	}
}

func TestServiceUsesConfiguredStateAndNeverCreatesProviderActions(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	review.Postings[0].Status, review.Postings[0].Substatus = orders.Present("cancelled"), orders.Present("cancelled_by_buyer")
	review.Config.StatusMappings = []orders.StatusMapping{{Status: "cancelled", StateID: "generic-cancelled"},
		{Status: "cancelled", Substatus: orders.Present("cancelled_by_buyer"), StateID: "native-buyer-cancelled"}}
	service := flowService(t, &flowStore{review: review}, flowResolver{})
	preview := flowPreview(t, service, review)
	command := preview.Decisions[0].Commands[0]
	if command.Action != orders.ActionMirrorCreate || command.CustomerOrder.StateID != "native-buyer-cancelled" || command.PostingAction != nil {
		t.Fatalf("source status became a provider action or guessed state: %+v", command)
	}
	preview, err := service.Preview(context.Background(), orders.PreviewRequest{Scope: review.Config.Scope, ExpectedRevision: 1,
		Actor: "authenticated-owner", Actions: []orders.Action{orders.ActionPostingCancel}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 0 {
		t.Fatal("unverified outgoing cancellation became dispatchable")
	}
}

func TestServiceWhitelistAndMissingFactsDoNotClearTargetFields(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	review.Config.FieldMappings = append(review.Config.FieldMappings, orders.FieldMapping{Source: "order_number", Target: "description"})
	review.Postings[0].OrderNumber = orders.Field[string]{Presence: orders.PresenceNull}
	review.Postings[0].PII = &orders.Customer{Name: orders.Present("PRIVATE_CUSTOMER")}
	preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
	write := preview.Decisions[0].Commands[0].CustomerOrder
	if write.Description.IsSpecified() || preview.Decisions[0].Posting.PII != nil {
		t.Fatal("missing fact requested a clear or PII remained in the preview")
	}
	raw, err := json.Marshal(preview)
	if err != nil || strings.Contains(string(raw), "PRIVATE_CUSTOMER") {
		t.Fatal("customer PII was serialized")
	}
	review.Config.FieldMappings = slices.DeleteFunc(review.Config.FieldMappings, func(mapping orders.FieldMapping) bool { return mapping.Target == "positions.quantity" })
	preview = flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
	if preview.Decisions[0].Ready || !slices.ContainsFunc(preview.Decisions[0].Validation, func(value orders.Validation) bool { return value.Code == "field_not_authorized" }) {
		t.Fatal("unreviewed position quantity was writable")
	}
}

func TestServiceExistingOrderNoopAndScalarUpdatePreserveManualPositions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		change  func(*orders.TargetObservation)
		command bool
		ready   bool
	}{{name: "already represented", ready: true}, {name: "state with manual position", command: true, ready: true, change: changeTargetStateAndPositions},
		{name: "position replacement", change: func(target *orders.TargetObservation) { target.Positions[0].Quantity = "3" }},
		{name: "malformed native field presence", change: func(target *orders.TargetObservation) {
			target.Fields.Name = orders.Field[string]{Presence: orders.PresenceValue}
		}}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			review, target := existingFlowReview(t)
			if test.change != nil {
				test.change(&target)
			}
			provider := &flowProvider{target: target}
			preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{target: provider}), review)
			decision := preview.Decisions[0]
			if decision.Ready != test.ready || (len(decision.Commands) == 1) != test.command {
				t.Fatalf("unexpected existing target decision: %+v", decision)
			}
			if test.command && (!slices.Equal(decision.Commands[0].CustomerOrder.OwnedFields, []string{"state"}) || !decision.Commands[0].TargetPositionsComplete) {
				t.Fatalf("scalar update owns manual positions: %+v", decision.Commands[0])
			}
		})
	}
}

func existingFlowReview(t *testing.T) (orders.ReviewContext, orders.TargetObservation) {
	t.Helper()
	review := flowReview(t, "posting")
	preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
	target := flowTarget(preview.Decisions[0].Commands[0])
	review.Correlations[0] = target.Correlation
	return review, target
}

func changeTargetStateAndPositions(target *orders.TargetObservation) {
	target.Fields.StateID = "manual-old-state"
	target.Positions = append(target.Positions, orders.TargetPosition{ID: "manual-position", MoyskladKind: "product", MoyskladID: "manual-product", Quantity: "10"})
}

func TestServiceApprovalSelectsReadyCommandsAndHonorsScopeGate(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	store := &flowStore{review: review}
	service := flowService(t, store, flowResolver{})
	preview := flowPreview(t, service, review)
	preview.Decisions = append(preview.Decisions, orders.Decision{Posting: orders.Posting{PostingNumber: "blocked"}}, orders.Decision{Posting: orders.Posting{PostingNumber: "noop"}, Ready: true})
	store.previews[preview.ID] = preview
	_, err := service.Approve(context.Background(), orders.Approval{Scope: preview.Scope, PreviewID: preview.ID, Actor: "authenticated-owner", IdempotencyKey: "approval"})
	if err != nil || !slices.Equal(store.approval.PostingNumbers, []string{"posting"}) {
		t.Fatalf("approval did not isolate ready commands: %+v, %v", store.approval, err)
	}
	service.options.AllowScope = nil
	_, err = service.Approve(context.Background(), orders.Approval{Scope: preview.Scope, PreviewID: preview.ID, Actor: "authenticated-owner", IdempotencyKey: "other"})
	if !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("scope gate did not block approval: %v", err)
	}
	if _, err = service.Config(context.Background(), review.Config.Scope); err != nil {
		t.Fatalf("read-only configuration was gated: %v", err)
	}
}

func TestServiceUnknownFenceBlocksOnlyItsPostingBeforeTargetReads(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "healthy-posting")
	fenced := review.Postings[0]
	fenced.PostingNumber = "unknown-posting"
	review.Postings = append(review.Postings, fenced)
	review.ActivePostings = map[string]orders.State{fenced.PostingNumber: orders.StateUnknown}
	preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
	if !preview.Decisions[0].Ready || preview.Decisions[1].Ready || len(preview.Decisions[1].Commands) != 0 {
		t.Fatalf("unknown target fenced independent work: %+v", preview.Decisions)
	}
	if !slices.ContainsFunc(preview.Decisions[1].Validation, func(value orders.Validation) bool { return value.Code == "target_fenced_unknown_or_pending" }) {
		t.Fatal("unknown target fence was not explained")
	}
}
