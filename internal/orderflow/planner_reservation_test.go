package orderflow

import (
	"slices"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestMirrorReservePolicyIsExplicitAndIndependentOfMappingOrder(t *testing.T) {
	t.Parallel()
	for _, first := range []bool{false, true} {
		t.Run(map[bool]string{true: "reserve first", false: "reserve last"}[first], func(t *testing.T) {
			t.Parallel()
			review := flowReview(t, "reserve-posting")
			review.Config.FieldMappings = slices.DeleteFunc(review.Config.FieldMappings, func(m orders.FieldMapping) bool { return m.Target == "positions.reserve" || m.Target == "applicable" })
			mapping := orders.FieldMapping{Source: orders.ReserveOpenQuantitySource, Target: "positions.reserve"}
			if first {
				review.Config.FieldMappings = append([]orders.FieldMapping{mapping}, review.Config.FieldMappings...)
			}
			review.Config.FieldMappings = append(review.Config.FieldMappings, orders.FieldMapping{Source: "constant.true", Target: "applicable"})
			if !first {
				review.Config.FieldMappings = append(review.Config.FieldMappings, mapping)
			}
			preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
			decision := preview.Decisions[0]
			if !decision.Ready || len(decision.Commands) != 1 {
				t.Fatalf("explicit policy blocked: %+v", decision.Validation)
			}
			position := decision.Commands[0].CustomerOrder.Positions[0]
			if !position.Reserve.IsKnown() || !orders.ExactDecimalEqual(*position.Reserve.Value, position.Quantity) {
				t.Fatal("normal service did not project reserve")
			}
		})
	}
}

func TestMirrorReservePolicyDoesNotBecomeDefaultOrGuessUnsupportedObligations(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"awaiting_packaging", "delivering", "delivered", "future_status", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			review := flowReview(t, "reserve-posting")
			review.Config.FieldMappings = slices.DeleteFunc(review.Config.FieldMappings, func(m orders.FieldMapping) bool { return m.Target == "positions.reserve" })
			review.Postings[0].Status = orders.Present(status)
			if status == "awaiting_packaging" {
				preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
				if !preview.Decisions[0].Ready || preview.Decisions[0].Commands[0].CustomerOrder.Positions[0].Reserve.IsSpecified() {
					t.Fatal("reserve policy silently enabled")
				}
			}
			review.Config.FieldMappings = append(review.Config.FieldMappings, orders.FieldMapping{Source: orders.ReserveOpenQuantitySource, Target: "positions.reserve"})
			preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
			if status != "awaiting_packaging" && (preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 0) {
				t.Fatalf("unsupported obligation reserved: %s", status)
			}
		})
	}
}

func TestLocalSplitReserveDependencyBlocksOnlyAffectedReservation(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "split-child")
	review.Config.FieldMappings = slices.DeleteFunc(review.Config.FieldMappings, func(m orders.FieldMapping) bool {
		return m.Target == "positions.reserve" || m.Target == "applicable"
	})
	review.Config.FieldMappings = append(review.Config.FieldMappings,
		orders.FieldMapping{Source: orders.ReserveOpenQuantitySource, Target: "positions.reserve"},
		orders.FieldMapping{Source: "constant.true", Target: "applicable"})
	review.SplitReserveBlocked = map[string]bool{"split-child": true}
	preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
	decision := preview.Decisions[0]
	if decision.Ready || len(decision.Commands) != 0 || !slices.ContainsFunc(decision.Validation, func(v orders.Validation) bool {
		return v.Code == "split_reserve_transfer_pending"
	}) {
		t.Fatal("local split dependency was ignored", decision.Validation)
	}
	review.SplitReserveBlocked = map[string]bool{"other-child": true}
	preview = flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
	if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 {
		t.Fatal("independent posting blocked", preview.Decisions[0].Validation)
	}
}
