package orderactions

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

var rfbsTestNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func rfbsTestPosting() orders.Posting {
	profile := orders.CapabilityRef{ID: "profile-rfbs", Revision: 7, WarehouseID: "1020001323428000", DeliveryMethodID: "9007199254740993",
		Scheme: "rFBS", Carrier: orders.Present("non_integrated"), Destination: orders.Present("customer"), Express: orders.Present(false),
		VerifiedDimensions: []string{"scheme", "warehouse", "carrier", "express", "destination"},
		ObservedAt:         rfbsTestNow.Add(-time.Minute), ValidUntil: rfbsTestNow.Add(4 * time.Minute)}
	return orders.Posting{Scope: orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"},
		CommercialOrderID: "order-1", PostingNumber: "posting-1", Scheme: orders.Present("rFBS"), Status: orders.Present("awaiting_deliver"),
		WarehouseID: orders.Present(profile.WarehouseID), DeliveryMethodID: orders.Present(profile.DeliveryMethodID), DeliveryProfile: &profile,
		Delivery:         orders.DeliveryFacts{IntegrationType: orders.Present("non_integrated"), Express: orders.Present(false)},
		AvailableActions: []string{"LOCAL_FIXTURE_ACTION"},
		Lines:            []orders.PostingLine{{Identity: orders.LineIdentity{SKU: "123", OfferID: "offer-1"}, Quantity: "1"}},
		Evidence:         orders.EvidenceRevision{Revision: "source-r1", Origin: orders.OriginOzon, ObservedAt: rfbsTestNow, CheckedAt: rfbsTestNow}}
}

func rfbsTestCommand(t *testing.T, posting orders.Posting, action orders.Action) orders.Command {
	t.Helper()
	correlation, err := orders.CorrelationFor(posting.Scope, posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	profile := *posting.DeliveryProfile
	return orders.Command{ID: "command-1", Scope: posting.Scope, Action: action, PostingNumber: posting.PostingNumber,
		CommercialOrderID: posting.CommercialOrderID, Correlation: correlation, ConfigRevision: 1,
		Credentials: orders.CredentialRevisions{Ozon: 1, Moysklad: 1}, ExpectedSourceRevision: posting.Evidence.Revision,
		SourceOrigin: orders.OriginUser, IdempotencyKey: "durable-intent-1", CreatedAt: rfbsTestNow,
		Warehouse: orders.WarehouseMappingRef{ID: "warehouse-map-1", Version: 1, Scope: posting.Scope,
			OzonWarehouseID: profile.WarehouseID, MoyskladStoreID: "store-1", Confirmed: true, ConfirmedAt: rfbsTestNow, Capability: profile},
		PostingAction: &orders.PostingAction{Capability: profile}}
}

func rfbsHasDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func rfbsPVZPosting(partner bool) orders.Posting {
	posting := rfbsTestPosting()
	posting.Delivery.IntegrationType = orders.Present("hybrid")
	posting.Delivery.Express = orders.Field[bool]{Presence: orders.PresenceNull}
	posting.Delivery.ProviderID = orders.Field[string]{Presence: orders.PresenceNull}
	posting.DeliveryProfile.Destination = orders.Present("ozon_pvz")
	if partner {
		posting.Delivery.TPLProviderID = orders.Present("9007199254740995")
		posting.DeliveryProfile.Carrier = orders.Present("integrated")
	}
	return posting
}
