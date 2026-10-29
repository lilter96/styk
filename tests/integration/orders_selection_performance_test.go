//go:build integration

package integration

import (
	"fmt"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func BenchmarkOrdersBoundedReviewSelection(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			fixture := selectionOrdersFixture(b)
			seedOrdersBenchmark(b, fixture, size)
			if _, err := fixture.db.Pool.Exec(fixture.ctx, `ANALYZE orders_posting_heads,orders_posting_observations,orders_projection_bindings,orders_previews,orders_work`); err != nil {
				b.Fatal(err)
			}
			b.Run("full_snapshot_then_100", func(b *testing.B) {
				benchmarkOrderReviewSelection(b, fixture, false)
			})
			b.Run("bounded_100", func(b *testing.B) {
				benchmarkOrderReviewSelection(b, fixture, true)
			})
		})
	}
}

func benchmarkOrderReviewSelection(b *testing.B, fixture ordersStoreFixture, bounded bool) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		var review orders.ReviewContext
		var err error
		if bounded {
			review, err = fixture.db.OrderReviewContextFor(fixture.ctx, fixture.config.Scope, nil, false)
		} else {
			review, err = fixture.db.OrderReviewContext(fixture.ctx, fixture.config.Scope)
			review.Postings = review.Postings[:min(len(review.Postings), orders.MaxPagePostings)]
		}
		if err != nil || len(review.Postings) != orders.MaxPagePostings || bounded && len(review.Correlations) != orders.MaxPagePostings {
			b.Fatalf("review selection: postings=%d correlations=%d error=%v", len(review.Postings), len(review.Correlations), err)
		}
	}
}
