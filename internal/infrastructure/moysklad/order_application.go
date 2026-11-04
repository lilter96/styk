package moysklad

import (
	"context"
	"github.com/lilter96/styk/internal/orders"
)

// This read occurs before every scalar application write, including attribute
// merge paths. A provider without conditional PUT still has a remote race after
// this final snapshot; an unchanged line collection alone is not ownership.
func (c *OrderClient) applicationPreflight(ctx context.Context, command orders.Command) error {
	if !command.WritesApplication() {
		return nil
	}
	target, err := c.readCustomerOrder(ctx, command.Correlation)
	if err != nil {
		return err
	}
	return orders.ApplicationTargetCurrent(command, target)
}
