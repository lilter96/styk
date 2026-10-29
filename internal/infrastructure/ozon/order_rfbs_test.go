package ozon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orders"
)

var rfbsAdapterNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func TestRFBSReaderPreservesIntegrationAndProviderNamespaces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, facts      string
		integration      orders.Presence
		express          orders.Presence
		provider, tpl    orders.Presence
		tplValue         string
		explicitNoAction bool
	}{
		{"seller hybrid", `"tpl_integration_type":"hybrid","is_express":null,"available_actions":[],`, orders.PresenceValue, orders.PresenceNull, orders.PresenceNull, orders.PresenceMissing, "", true},
		{"partner hybrid", `"tpl_integration_type":"hybrid","is_express":false,`, orders.PresenceValue, orders.PresenceZero, orders.PresenceNull, orders.PresenceValue, "9007199254740995", false},
		{"missing is not null", "", orders.PresenceMissing, orders.PresenceMissing, orders.PresenceNull, orders.PresenceMissing, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path != orderGetPath {
					t.Error("unexpected provider method")
				}
				tpl := ""
				if tc.tplValue != "" {
					tpl = `,"tpl_provider_id":"` + tc.tplValue + `"`
				}
				writeTestResponse(t, w, `{"result":{`+tc.facts+`"posting_number":"posting-1","order_id":"1","scheme":"rFBS","status":"awaiting_deliver","delivery_method":{"warehouse_id":"1020001323428000","id":"9007199254740993","provider_id":null`+tpl+`},"products":[{"offer_id":"offer-1","quantity":1}]}}`)
			}))
			defer server.Close()
			reader := newOrderTestClient(t, server, OrderReadOptions{})
			posting, err := reader.ReadOrderPosting(context.Background(), orderTestScope(), "posting-1")
			if err != nil || calls.Load() != 1 {
				t.Fatalf("bounded read failed: calls=%d error=%v", calls.Load(), err)
			}
			assertRFBSReadFields(t, posting, tc.integration, tc.express, tc.provider, tc.tpl, tc.tplValue)
			if tc.explicitNoAction && (posting.AvailableActions == nil || len(posting.AvailableActions) != 0) {
				t.Fatal("explicit empty available action list became missing")
			}
		})
	}
}

func assertRFBSReadFields(t *testing.T, posting orders.Posting, integration, express, provider, tpl orders.Presence, tplValue string) {
	t.Helper()
	if posting.Delivery.IntegrationType.Presence != integration || posting.Delivery.Express.Presence != express {
		t.Fatal("integration or express presence was flattened")
	}
	if posting.Delivery.ProviderID.Presence != provider || posting.Delivery.TPLProviderID.Presence != tpl || orderKnown(posting.Delivery.TPLProviderID) != tplValue {
		t.Fatal("provider_id and tpl_provider_id were aliased")
	}
	if orderKnown(posting.WarehouseID) != "1020001323428000" || orderKnown(posting.DeliveryMethodID) != "9007199254740993" {
		t.Fatal("warehouse and delivery method identities were conflated")
	}
	if !hasOrderDrift(posting, "schema_unverified") {
		t.Fatal("new typed read facts claimed a verified provider schema")
	}
}

func TestRFBSAdapterPlanningNeverDispatchesHTTP(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeTestResponse(t, w, `{}`)
	}))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{})
	reader.now = func() time.Time { return rfbsAdapterNow }
	posting := rfbsAdapterPosting()
	command := rfbsAdapterCommand(t, posting)
	plan, err := reader.PlanRFBSOrder(posting, command)
	if !errors.Is(err, orders.ErrBlocked) || plan.Allowed || len(plan.Body) != 0 || plan.Endpoint != "/v2/fbs/posting/delivering" {
		t.Fatalf("unexpected action plan: allowed=%v endpoint=%s error=%v", plan.Allowed, plan.Endpoint, err)
	}
	graph, err := reader.RFBSOrderGraph(posting)
	if err != nil || len(graph) != 9 {
		t.Fatalf("missing deterministic graph: count=%d error=%v", len(graph), err)
	}
	for _, decision := range graph {
		if decision.Allowed || decision.Route != orderactions.RFBSRouteSellerPVZ {
			t.Fatalf("unverified operation executable or wrong workflow: %+v", decision)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("read-only action inspection dispatched HTTP")
	}
}

func TestRFBSAdapterRejectsCrossTenantPlanning(t *testing.T) {
	t.Parallel()
	reader := &OrdersClient{scope: orderTestScope(), now: func() time.Time { return rfbsAdapterNow }}
	posting := rfbsAdapterPosting()
	command := rfbsAdapterCommand(t, posting)
	posting.Scope.Tenant = "different-tenant"
	if _, err := reader.PlanRFBSOrder(posting, command); !errors.Is(err, orders.ErrInvalid) {
		t.Fatal("cross-tenant plan accepted")
	}
	if _, err := reader.RFBSOrderGraph(posting); !errors.Is(err, orders.ErrInvalid) {
		t.Fatal("cross-tenant graph accepted")
	}
}

func rfbsAdapterPosting() orders.Posting {
	profile := orders.CapabilityRef{ID: "profile-rfbs", Revision: 1, WarehouseID: "warehouse-1", DeliveryMethodID: "method-1",
		Scheme: "rFBS", Carrier: orders.Present("non_integrated"), Destination: orders.Present("ozon_pvz"), Express: orders.Present(false),
		VerifiedDimensions: []string{"scheme", "warehouse", "carrier", "express", "destination"},
		ObservedAt:         rfbsAdapterNow, ValidUntil: rfbsAdapterNow.Add(5 * time.Minute)}
	return orders.Posting{Scope: orderTestScope(), CommercialOrderID: "order-1", PostingNumber: "posting-1", Scheme: orders.Present("rFBS"),
		Status: orders.Present("awaiting_deliver"), WarehouseID: orders.Present(profile.WarehouseID), DeliveryMethodID: orders.Present(profile.DeliveryMethodID), DeliveryProfile: &profile,
		Delivery: orders.DeliveryFacts{IntegrationType: orders.Present("hybrid"), Express: orders.Field[bool]{Presence: orders.PresenceNull}, ProviderID: orders.Field[string]{Presence: orders.PresenceNull}},
		Evidence: orders.EvidenceRevision{Revision: "source-1", Origin: orders.OriginOzon, ObservedAt: rfbsAdapterNow, CheckedAt: rfbsAdapterNow}}
}

func rfbsAdapterCommand(t *testing.T, posting orders.Posting) orders.Command {
	t.Helper()
	correlation, err := orders.CorrelationFor(posting.Scope, posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	profile := *posting.DeliveryProfile
	return orders.Command{ID: "command-1", Scope: posting.Scope, Action: orders.ActionPostingShip,
		PostingNumber: posting.PostingNumber, CommercialOrderID: posting.CommercialOrderID, ExpectedSourceRevision: posting.Evidence.Revision,
		ConfigRevision: 1, Credentials: orders.CredentialRevisions{Ozon: 1, Moysklad: 1}, SourceOrigin: orders.OriginUser,
		IdempotencyKey: "intent-1", CreatedAt: rfbsAdapterNow, Correlation: correlation,
		Warehouse: orders.WarehouseMappingRef{ID: "map-1", Version: 1, Scope: posting.Scope, OzonWarehouseID: profile.WarehouseID,
			MoyskladStoreID: "store-1", Confirmed: true, ConfirmedAt: rfbsAdapterNow, Capability: profile},
		PostingAction: &orders.PostingAction{Capability: profile}}
}
