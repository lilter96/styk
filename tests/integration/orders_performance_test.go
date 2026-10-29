//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func BenchmarkOrdersStoreSnapshotSizes(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			fixture := newOrdersStoreFixture(b)
			started := time.Now()
			seedOrdersBenchmark(b, fixture, size)
			seedDuration := time.Since(started)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				page, err := fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, orders.PageRequest{Limit: 100})
				if err != nil || len(page.Postings) != 100 {
					b.Fatalf("bounded posting page: count=%d error=%v", len(page.Postings), err)
				}
				current, err := fixture.db.OrderReviewContext(fixture.ctx, fixture.config.Scope)
				if err != nil || len(current.Postings) != size {
					b.Fatalf("review context: count=%d error=%v", len(current.Postings), err)
				}
			}
			b.ReportMetric(seedDuration.Seconds(), "seed_s")
		})
	}
}

func seedOrdersBenchmark(b *testing.B, fixture ordersStoreFixture, size int) {
	b.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	lease := ordersIngestionLease(b, fixture, "benchmark", "seed", 5*time.Minute)
	var revision int64
	cursor := ""
	for start := 0; start < size; start += 100 {
		postings := make([]orders.Posting, 0, 100)
		for index := start; index < start+100; index++ {
			postings = append(postings, ordersStorePosting(fixture.config.Scope, fmt.Sprintf("benchmark-%06d", index), "source-1", now))
		}
		complete := start+100 == size
		next := fmt.Sprint(start + 100)
		if complete {
			next = ""
		}
		page := orders.SourcePage{Scope: fixture.config.Scope, ConfigRevision: lease.ConfigRevision, Credentials: lease.Credentials, Stream: "benchmark", RefreshKey: "seed", ExpectedCheckpointRevision: revision, ExpectedCursor: cursor, NextCursor: next, Complete: complete, WindowFrom: now.Add(-time.Hour), WindowTo: now, Postings: postings, StartedAt: now, FinishedAt: now, IdempotencyKey: fmt.Sprintf("seed-%d", start), FenceToken: lease.Token}
		checkpoint, err := fixture.db.CommitOrderPage(fixture.ctx, page)
		if err != nil {
			b.Fatal(err)
		}
		revision, cursor = checkpoint.Revision, checkpoint.Cursor
	}
}
