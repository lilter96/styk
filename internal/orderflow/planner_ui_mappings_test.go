package orderflow

import (
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestPreviewAcceptsMerchantPositionMappingAliases(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"lines.", "products.", "positions.", "posting.positions."} {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()
			review := flowReview(t, "merchant-config-posting")
			for index := range review.Config.FieldMappings {
				mapping := &review.Config.FieldMappings[index]
				if mapping.Target == "positions.quantity" || mapping.Target == "positions.price" {
					mapping.Source = prefix + strings.TrimPrefix(mapping.Target, "positions.")
				}
			}
			preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
			decision := preview.Decisions[0]
			if !decision.Ready || len(decision.Commands) != 1 || len(decision.Validation) != 0 {
				t.Fatalf("merchant position mappings rejected: %+v", decision)
			}
			write := decision.Commands[0].CustomerOrder
			if write == nil || len(write.Positions) != 1 || write.Positions[0].Quantity != review.Postings[0].Lines[0].Quantity {
				t.Fatal("alias changed authoritative source quantities")
			}
		})
	}
}

func TestPositionMappingAliasesDoNotAuthorizeInventedReserveOrDifferentField(t *testing.T) {
	t.Parallel()
	for _, mapping := range []orders.FieldMapping{
		{Source: "positions.price", Target: "positions.quantity"},
		{Source: "positions.reserve", Target: "positions.reserve"},
		{Source: "positions.unknown", Target: "positions.quantity"},
	} {
		t.Run(mapping.Source+mapping.Target, func(t *testing.T) {
			t.Parallel()
			if validation := applyPositionMapping(&orders.CustomerOrderWrite{}, mapping); validation.Code == "" {
				t.Fatal("unsupported position source was accepted")
			}
		})
	}
}
