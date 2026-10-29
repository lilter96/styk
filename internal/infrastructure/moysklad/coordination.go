package moysklad

import (
	"context"
	"github.com/lilter96/styk/internal/providercall"
)

// Coordinate binds admission to credentials already resolved for this account.
// It must be called before the client is shared or any provider request is made.
func (c *Client) Coordinate(gate providercall.Gate, scope providercall.Scope) error {
	client, err := providercall.Bind(c.http, gate, scope, c.http.Timeout)
	if err != nil {
		return err
	}
	c.http = client
	c.coordinated = true
	return nil
}

func (c *CatalogClient) Coordinate(gate providercall.Gate, scope providercall.Scope) error {
	return c.client.Coordinate(gate, scope)
}

func (c *Client) authorizeRequest(ctx context.Context, guard func(context.Context) error) (context.Context, error) {
	if c.coordinated {
		return providercall.WithGuard(ctx, guard), nil
	}
	return ctx, guard(ctx)
}
