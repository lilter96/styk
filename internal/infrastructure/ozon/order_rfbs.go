package ozon

import (
	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orders"
)

// PlanRFBSOrder is a read-only inspection of an explicit command and posting.
// Blocked plans cannot reach the provider transport or create a request body.
func (c *OrdersClient) PlanRFBSOrder(posting orders.Posting, command orders.Command) (orderactions.RequestPlan, error) {
	if posting.Scope != c.scope || command.Scope != c.scope {
		return orderactions.RequestPlan{}, orders.ErrInvalid
	}
	return orderactions.PlanRFBSAt(posting, command, c.now())
}

func (c *OrdersClient) RFBSOrderGraph(posting orders.Posting) ([]orderactions.RFBSActionDecision, error) {
	if posting.Scope != c.scope {
		return nil, orders.ErrInvalid
	}
	return orderactions.RFBSStandardGraph(posting, c.now())
}
