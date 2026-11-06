package orderflow

import (
	"fmt"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestMirrorRoutesEveryPostingByItsOwnWarehouse(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting-1")
	review.Config.StoreID = "legacy-ignored"
	for index := 2; index <= 3; index++ {
		posting := review.Postings[0]
		posting.PostingNumber = fmt.Sprintf("posting-%d", index)
		posting.WarehouseID = orders.Present(fmt.Sprintf("warehouse-%d", index))
		mapping := review.WarehouseMappings[0]
		mapping.ID = fmt.Sprint(index)
		mapping.OzonWarehouseID = *posting.WarehouseID.Value
		mapping.MoyskladStoreID = fmt.Sprintf("store-%d", index)
		review.Postings = append(review.Postings, posting)
		review.WarehouseMappings = append(review.WarehouseMappings, mapping)
	}
	missing := review.Postings[0]
	missing.PostingNumber = "unmapped"
	missing.WarehouseID = orders.Present("unknown")
	review.Postings = append(review.Postings, missing)
	preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
	for index, decision := range preview.Decisions {
		if index == 3 {
			if decision.Ready || len(decision.Commands) != 0 {
				t.Fatal("unmapped posting escaped review")
			}
			continue
		}
		if !decision.Ready || len(decision.Commands) != 1 {
			t.Fatalf("mapped posting blocked: %+v", decision.Validation)
		}
		command := decision.Commands[0]
		if command.CustomerOrder.StoreID != review.WarehouseMappings[index].MoyskladStoreID {
			t.Fatalf("posting routed to %q", command.CustomerOrder.StoreID)
		}
	}
}

func TestPostingWarehouseRejectsAmbiguousUnconfirmedAndForeignEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*orders.ReviewContext)
	}{
		{"unconfirmed", func(r *orders.ReviewContext) { r.WarehouseMappings[0].Confirmed = false }},
		{"foreign", func(r *orders.ReviewContext) { r.WarehouseMappings[0].Scope.Tenant = "foreign" }},
		{"ambiguous", func(r *orders.ReviewContext) {
			r.WarehouseMappings = append(r.WarehouseMappings, r.WarehouseMappings[0])
		}},
		{"unknown", func(r *orders.ReviewContext) { r.Postings[0].WarehouseID = orders.Field[string]{} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := flowReview(t, "posting")
			tc.mutate(&r)
			_, issue := postingWarehouse(r.Postings[0], r)
			if issue.Code == "" {
				t.Fatal("invalid warehouse evidence allowed")
			}
		})
	}
}
