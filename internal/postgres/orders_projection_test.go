package postgres

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrderProjectionPreservesExactCapabilityRevision(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	posting := orders.Posting{Scope: orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}, PostingNumber: "posting", CommercialOrderID: "order",
		Lines:           []orders.PostingLine{{Identity: orders.LineIdentity{SKU: "9007199254740993"}, Quantity: "1"}},
		DeliveryProfile: &orders.CapabilityRef{ID: "capability", Revision: 7283753787198514456, WarehouseID: "warehouse", DeliveryMethodID: "method", Scheme: "FBS", ObservedAt: now, ValidUntil: now.Add(time.Hour)},
		Evidence:        orders.EvidenceRevision{Revision: "revision", Origin: orders.OriginOzon, ObservedAt: now}}
	next := posting
	next.Evidence.CheckedAt = now.Add(time.Minute)
	merged, err := mergeOrderPosting(posting, next)
	if err != nil || merged.DeliveryProfile.Revision != posting.DeliveryProfile.Revision {
		t.Fatalf("capability revision lost precision: %v", err)
	}
	before, err := orders.PostingFingerprint(posting)
	if err != nil {
		t.Fatal(err)
	}
	after, err := orders.PostingFingerprint(merged)
	if err != nil || before != after {
		t.Fatalf("unchanged projection changed fingerprint: %v", err)
	}
}
