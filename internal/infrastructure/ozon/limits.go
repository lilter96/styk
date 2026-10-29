package ozon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

// Limits bound one catalog traversal. Reaching a limit never returns a snapshot.
type Limits struct {
	Requests, Pages, Entities, Observations  int
	Attempts, Products, Warehouses, Postings int
	ResponseBytes                            int64
	Duration                                 time.Duration
}

type Counts struct{ Requests, Attempts, Pages, Entities, Products, Warehouses, Observations, Postings, CompletedRequests, CompletedPages, RateLimitResponses int }
type Progress func(context.Context, string, Counts) error
type LimitError struct {
	Resource string
	Counts   Counts
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("Ozon %s limit reached (requests=%d pages=%d entities=%d observations=%d)", e.Resource, e.Counts.Requests, e.Counts.Pages, e.Counts.Entities, e.Counts.Observations)
}

type budgetKey struct{}
type budget struct {
	limits       Limits
	counts       Counts
	progress     Progress
	phase        string
	lastProgress time.Time
}

// Progress callbacks run synchronously at phase changes and at most every two
// seconds at validated page boundaries; they never receive source data.
func (b *budget) report(ctx context.Context, phase string, force bool) error {
	changed := b.phase != phase
	b.phase = phase
	if b.progress == nil || (!changed && !force && time.Since(b.lastProgress) < 2*time.Second) {
		return nil
	}
	if err := b.progress(ctx, phase, b.counts); err != nil {
		return err
	}
	b.lastProgress = time.Now()
	return nil
}

func (b *budget) add(resource string, n int) error {
	var v *int
	var limit int
	switch resource {
	case "requests":
		v, limit = &b.counts.Requests, b.limits.Requests
	case "attempts":
		v, limit = &b.counts.Attempts, b.limits.Attempts
	case "pages":
		v, limit = &b.counts.Pages, b.limits.Pages
	case "entities":
		v, limit = &b.counts.Entities, b.limits.Entities
	case "products":
		v, limit = &b.counts.Products, b.limits.Products
	case "warehouses":
		v, limit = &b.counts.Warehouses, b.limits.Warehouses
	case "postings":
		v, limit = &b.counts.Postings, b.limits.Postings
	case "observations":
		v, limit = &b.counts.Observations, b.limits.Observations
	default:
		return errors.New("invalid budget resource")
	}
	if n > limit-*v {
		return &LimitError{Resource: resource, Counts: b.counts}
	}
	*v += n
	return nil
}

func (l Limits) validate() error {
	if l.Requests < 1 || l.Attempts < 1 || l.Pages < 1 || l.Entities < 1 || l.Products < 1 || l.Warehouses < 1 || l.Observations < 1 || l.Postings < 1 || l.ResponseBytes < 1 || l.ResponseBytes > maxBody || l.Duration <= 0 {
		return errors.New("invalid Ozon execution limits")
	}
	return nil
}

// ReadBounded performs the existing complete traversal with per-execution limits.
// A caller must not persist data when any error is returned.
func (c *Client) ReadBounded(ctx context.Context, limits Limits) (domain.OzonImport, Counts, error) {
	return c.ReadBoundedProgress(ctx, limits, nil)
}
func (c *Client) ReadBoundedProgress(ctx context.Context, limits Limits, progress Progress) (domain.OzonImport, Counts, error) {
	if err := limits.validate(); err != nil {
		return domain.OzonImport{}, Counts{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, limits.Duration)
	defer cancel()
	b := &budget{limits: limits, progress: progress}
	snap, err := c.Read(context.WithValue(ctx, budgetKey{}, b))
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return domain.OzonImport{}, b.counts, err
	}
	return snap, b.counts, nil
}
func (c *Client) ReadPostingsBounded(ctx context.Context, tenant string, since, until time.Time, limits Limits) (domain.PostingImport, Counts, error) {
	if err := limits.validate(); err != nil {
		return domain.PostingImport{}, Counts{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, limits.Duration)
	defer cancel()
	b := &budget{limits: limits}
	snap, err := c.ReadPostings(context.WithValue(ctx, budgetKey{}, b), tenant, since, until)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return domain.PostingImport{}, b.counts, err
	}
	return snap, b.counts, nil
}
