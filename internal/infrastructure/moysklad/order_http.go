package moysklad

import (
	"context"
	"net/http"
	"time"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

func orderHTTP(source *http.Client) (*http.Client, func()) {
	client := *source
	client.Timeout = 15 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport, sourceClose := orderHTTPTransport(source.Transport)
	client.Transport = transport
	return &client, sourceClose
}

func orderHTTPTransport(source http.RoundTripper) (http.RoundTripper, func()) {
	if source == nil {
		source = http.DefaultTransport
	}
	if bound, ok := source.(providercall.Transport); ok {
		base, closeHTTP := orderHTTPTransport(bound.Base)
		bound.Base, bound.Timeout = base, 15*time.Second
		return bound, closeHTTP
	}
	transport, ok := source.(*http.Transport)
	if !ok {
		return source, func() {}
	}
	clone := transport.Clone()
	clone.DisableKeepAlives = true
	clone.ForceAttemptHTTP2 = false
	clone.Protocols = new(http.Protocols)
	clone.Protocols.SetHTTP1(true)
	return clone, clone.CloseIdleConnections
}

func (c *OrderClient) finalOrderAuthorization(ctx context.Context, command orders.Command, authorize func(context.Context) (bool, error)) error {
	if !c.scopeAllowed(command.Scope) || authorize == nil {
		return ErrOrderIntent
	}
	allowed, err := authorize(ctx)
	if err != nil || !allowed || !c.scopeAllowed(command.Scope) {
		return ErrOrderIntent
	}
	return ctx.Err()
}
