package ordermoney

import "github.com/lilter96/styk/internal/orders"

func CommissionPostedExternally(plan orders.CommissionPlan, actual orders.CommissionObservation, acknowledgedID string) bool {
	return actual.Complete && actual.Revision != "" && !actual.ObservedAt.IsZero() && actual.Applicable && actual.Scope == plan.Scope && actual.ExternalCode == plan.ExternalCode && actual.DocumentID != "" && (acknowledgedID == "" || acknowledgedID == actual.DocumentID)
}
