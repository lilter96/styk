package ozon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orders"
)

func TestMethodCarrierEvidenceReachesNormalExecutionPlanner(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method, posting string
		allow, conflict bool
	}{{"non_integrated", "non_integrated", true, false}, {"aggregator", "aggregator", false, false}, {"3pl_tracking", "3pl_tracking", false, false}, {"hybrid", "hybrid", false, false}, {"non_integrated", "hybrid", false, true}, {"non_integrated", "aggregator", false, true}} {
		t.Run(tc.method+"_"+tc.posting, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case orderGetPath:
					writeTestResponse(t, w, fmt.Sprintf(`{"result":{"posting_number":"p","order_id":5,"status":"awaiting_deliver","tpl_integration_type":%q,"delivery_schema":"rfbs","delivery_method":{"id":8,"warehouse_id":7},"products":[{"sku":9,"offer_id":"offer","quantity":1,"price":"1.00","currency_code":"RUB"}],"available_actions":["non_int_delivering"]}}`, tc.posting))
				case orderWarehousePath:
					writeTestResponse(t, w, `{"warehouses":[{"warehouse_id":7,"warehouse_type":"rfbs","status":"created","is_rfbs":true,"is_express":false}],"has_next":false}`)
				case orderMethodsPath:
					writeTestResponse(t, w, fmt.Sprintf(`{"delivery_methods":[{"id":8,"warehouse_id":7,"is_express":false,"status":"ACTIVE","tpl_integration_type":%q}],"has_next":false}`, tc.method))
				default:
					t.Error("unexpected provider endpoint")
				}
			}))
			defer server.Close()
			client := newOrderTestClient(t, server, OrderReadOptions{ResolveDelivery: true})
			posting, err := client.ReadOrderPosting(context.Background(), orderTestScope(), "p")
			if err != nil {
				t.Fatal(err)
			}
			if orders.DeliveryIntegrationConflict(posting) != tc.conflict {
				t.Fatal("integration contradiction classification incorrect")
			}
			if tc.conflict {
				if posting.DeliveryProfile != nil {
					t.Fatal("contradictory carrier evidence retained authority")
				}
				// A previously enrolled mapping cannot bypass an explicit new contradiction.
				result, readErr := client.ListOrderDelivery(context.Background(), orderTestScope())
				if readErr != nil {
					t.Fatal(readErr)
				}
				for _, profile := range result.Profiles {
					if profile.CapabilityRef() != nil && orderactions.PlanExecution(posting, *profile.CapabilityRef(), orders.ActionPostingShip, time.Now()).Allowed {
						t.Fatal("stale mapping bypassed fresh conflict")
					}
				}
				return
			}
			if posting.DeliveryProfile == nil {
				t.Fatal("verified method identity missing")
			}
			plan := orderactions.PlanExecution(posting, *posting.DeliveryProfile, orders.ActionPostingShip, time.Now())
			if plan.Allowed != tc.allow {
				t.Fatalf("normal planner authority incorrect: %+v", plan.Blockers)
			}
			if tc.allow && !slices.Contains(posting.DeliveryProfile.VerifiedDimensions, "carrier") {
				t.Fatal("seller execution enabled without independent carrier dimension")
			}
		})
	}
}
