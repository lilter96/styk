package ozon

import (
	"context"
	"slices"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

// FBS methods are supplied by carriage discovery/get, whereas the generic
// delivery-method endpoint describes rFBS. Bind that identity to a fresh FBS
// warehouse observation; never infer non-Express from an absent method record.
func (c *OrdersClient) carriagePostingProfile(ctx context.Context, posting orders.Posting, method, warehouse string) (orders.Posting, error) {
	if posting.DeliveryProfile != nil {
		return posting, nil
	}
	if orderKnown(posting.Scheme) != "FBS" || orderKnown(posting.WarehouseID) != warehouse || orderKnown(posting.DeliveryMethodID) != method {
		return posting, nil
	}
	metadata, err := c.ListOrderDelivery(ctx, posting.Scope)
	if err != nil {
		return posting, err
	}
	for _, source := range metadata.Warehouses {
		if source.ID != warehouse {
			continue
		}
		if !source.IsRFBS.IsKnown() || *source.IsRFBS.Value || !source.Express.IsKnown() || *source.Express.Value || orderKnown(source.Type) != "fbs" {
			return posting, nil
		}
		posting.DeliveryProfile = &orders.CapabilityRef{ID: "carriage-fbs:" + warehouse + ":" + method, Revision: 1, WarehouseID: warehouse, DeliveryMethodID: method, Scheme: "FBS", Express: source.Express, ObservedAt: metadata.CheckedAt, ValidUntil: metadata.ValidUntil, VerifiedDimensions: []string{"scheme", "warehouse", "express"}}
		return posting, nil
	}
	return posting, nil
}

func carriageRelevantDrift(drift orders.Drift) bool {
	if strings.HasPrefix(drift.Code, "ORDERING_") || strings.HasPrefix(drift.Code, "conflicting_") {
		return true
	}
	for _, field := range []string{"requirements", "products", "available_actions", "delivery_method", "scheme", "status", "is_express", "is_rfbs", "tpl_integration"} {
		if strings.Contains(drift.Path, field) {
			return true
		}
	}
	return slices.Contains([]string{"invalid_json", "invalid_manifest", "unknown_contract"}, drift.Code)
}
