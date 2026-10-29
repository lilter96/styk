package moysklad

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

type LimitError struct{ Resource string }

func (e *LimitError) Error() string { return "MoySklad execution limit reached: " + e.Resource }

var ErrTransport = errors.New("MoySklad transport failure")

type budgetKey struct{}
type budget struct {
	http     *http.Client
	limits   domain.ImportLimits
	counts   domain.ImportCounts
	phase    string
	last     time.Time
	progress func(context.Context, string, domain.ImportCounts) error
}

func (b *budget) add(resource string) error {
	if b == nil {
		return nil
	}
	var n *int
	var max int
	switch resource {
	case "requests":
		n, max = &b.counts.Requests, b.limits.Requests
	case "attempts":
		n, max = &b.counts.Attempts, b.limits.Attempts
	case "pages":
		n, max = &b.counts.Pages, b.limits.Pages
	case "products":
		n, max = &b.counts.Products, b.limits.Products
	case "variants":
		n, max = &b.counts.Variants, b.limits.Variants
	case "warehouses":
		n, max = &b.counts.Warehouses, b.limits.Warehouses
	case "observations":
		n, max = &b.counts.Observations, b.limits.Observations
	case "warehouse_observations":
		n, max = &b.counts.WarehouseObservations, b.limits.WarehouseObservations
	default:
		return errors.New("invalid budget resource")
	}
	if *n >= max {
		return &LimitError{resource}
	}
	*n++
	return nil
}
func (b *budget) report(ctx context.Context, path string) error {
	if b == nil {
		return nil
	}
	phase := map[string]string{"entity/product": "READING_PRODUCTS", "entity/variant": "READING_VARIANTS", "entity/store": "READING_WAREHOUSES", "report/stock/all": "READING_AGGREGATE_STOCK", "report/stock/bystore": "READING_WAREHOUSE_STOCK"}[path]
	if b.progress != nil && (phase != b.phase || time.Since(b.last) >= 2*time.Second) {
		if err := b.progress(ctx, phase, b.counts); err != nil {
			return err
		}
		b.last = time.Now()
	}
	b.phase = phase
	return nil
}
func (c *Client) ReadBoundedProgress(ctx context.Context, account string, l domain.ImportLimits, progress func(context.Context, string, domain.ImportCounts) error) (domain.InventoryImport, domain.ImportCounts, error) {
	if !l.ValidFor("moysklad") {
		return domain.InventoryImport{}, domain.ImportCounts{}, errors.New("invalid MoySklad limits")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(l.DurationNS))
	defer cancel()
	// GETs on a reused connection can be transparently replayed by Transport.
	// Use fresh connections for bounded traversals so every transport send is
	// admitted by the explicit attempt budget. Keep the shared rate limiter.
	hc := *c.http
	transport := hc.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if tr, ok := transport.(*http.Transport); ok {
		clone := tr.Clone()
		clone.DisableKeepAlives = true
		hc.Transport = clone
		defer clone.CloseIdleConnections()
	}
	b := &budget{limits: l, progress: progress, http: &hc}
	snap, err := c.Read(context.WithValue(ctx, budgetKey{}, b), account)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return snap, b.counts, err
}

// ReadCustomerOrdersBounded uses the same request, response and duration
// limits as a catalog traversal. Incomplete traversals never return a batch.
func (c *Client) ReadCustomerOrdersBounded(ctx context.Context, tenant, account string, since, until time.Time, l domain.ImportLimits) (domain.CustomerOrderImport, domain.ImportCounts, error) {
	if !l.ValidFor("moysklad") {
		return domain.CustomerOrderImport{}, domain.ImportCounts{}, errors.New("invalid MoySklad limits")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(l.DurationNS))
	defer cancel()
	hc := *c.http
	transport := hc.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if tr, ok := transport.(*http.Transport); ok {
		clone := tr.Clone()
		clone.DisableKeepAlives = true
		hc.Transport = clone
		defer clone.CloseIdleConnections()
	}
	b := &budget{limits: l, http: &hc}
	ctx = context.WithValue(ctx, orderPageSizeKey{}, 100)
	snap, err := c.ReadCustomerOrders(context.WithValue(ctx, budgetKey{}, b), tenant, account, since, until)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		return domain.CustomerOrderImport{}, b.counts, err
	}
	return snap, b.counts, nil
}
