package postgres

import (
	"github.com/lilter96/styk/internal/orders"
	"testing"
	"time"
)

func TestPostingMergeDoesNotRestoreContradictedCarrierAuthority(t *testing.T) {
	t.Parallel()
	previous := orders.Posting{DeliveryProfile: &orders.CapabilityRef{ID: "prior", Revision: 1, WarehouseID: "warehouse", DeliveryMethodID: "method", Scheme: "rFBS", Carrier: orders.Present("non_integrated"), ObservedAt: time.Now(), ValidUntil: time.Now().Add(time.Minute)}}
	next := orders.Posting{Drift: []orders.Drift{{Path: "$.tpl_integration_type", Code: "conflicting_integration"}}}
	actual, err := mergeOrderPosting(previous, next)
	if err != nil || actual.DeliveryProfile != nil || !orders.DeliveryIntegrationConflict(actual) {
		t.Fatalf("previous authority resurrected after contradiction: %+v %v", actual, err)
	}
	next.Drift = nil
	actual, err = mergeOrderPosting(previous, next)
	if err != nil || actual.DeliveryProfile == nil {
		t.Fatal("ordinary missing observation erased prior evidence")
	}
}
