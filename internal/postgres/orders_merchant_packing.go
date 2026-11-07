package postgres

import "github.com/lilter96/styk/internal/orders"

func validateMerchantPackingBaseline(command orders.Command, current orders.ReviewContext) error {
	if command.MerchantIntent == nil || command.Action != orders.ActionPostingPack {
		return nil
	}
	baseline, found := current.PositionBaselines[command.PostingNumber]
	if !found || command.MerchantPositionBaseline == nil || !baseline.Equal(*command.MerchantPositionBaseline) || baseline.Correlation != command.Correlation {
		return orders.ErrStale
	}
	for _, binding := range baseline.Bindings {
		if !orderProductMappingPresent(binding.Source.Mapping, current.ProductMappings) {
			return orders.ErrStale
		}
	}
	return nil
}
