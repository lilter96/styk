package orderinventory

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

func TestNativePointExpiredProofCannotBorrowFreshAggregateReserve(t *testing.T) {
	t.Parallel()
	input := nativePointAttributionFixture()
	observation := &input.Observations[0]
	observation.Posting.Evidence.ObservedAt = input.Now.Add(-26 * time.Hour)
	observation.Posting.Evidence.CheckedAt = input.Now.Add(-time.Minute)
	observation.Target.ObservedAt = input.Now.Add(-25 * time.Hour)
	observation.ReconciledAt = input.Now.Add(-25 * time.Hour)
	observation.NativePointAdmittedAt = input.Now.Add(-24 * time.Hour)
	observation.NativeStartedAt = observation.NativePointAdmittedAt.Add(time.Second)
	observation.NativeFinishedAt = observation.NativeStartedAt.Add(time.Second)
	observation.NativePointRecordedAt = observation.NativeFinishedAt.Add(time.Second)
	for index := range observation.Positions {
		observation.Positions[index].ObservedAt = observation.NativeFinishedAt
	}
	// The original owned reserve3 may have been released since this point read;
	// a different merchant reserve3 can leave the aggregate baseline unchanged.
	// Fresh physical10/reserve3 cannot prove that this Ozon debt remains covered.
	guard, decision := Assess(input), inventoryDecision(input)
	if !guard.Blocked || decision.Status != domain.StockBlocked || decision.Quantity != nil {
		quantity := "unknown"
		if decision.Quantity != nil {
			quantity = *decision.Quantity
		}
		t.Fatalf("expired point exposed unverified reserve: guard=%+v status=%s quantity=%s", guard, decision.Status, quantity)
	}
	if *input.Baseline.Physical != "10" || *input.Baseline.Reserve != "3" {
		t.Fatal("freshness enforcement changed source stock arithmetic")
	}
}

func TestNativePointFreshnessBoundaryIsNotRenewedByStockTraversal(t *testing.T) {
	t.Parallel()
	for _, age := range []time.Duration{15*time.Minute - time.Nanosecond, 15 * time.Minute, 15*time.Minute + time.Nanosecond} {
		t.Run(age.String(), func(t *testing.T) {
			t.Parallel()
			input := nativePointAttributionFixture()
			observation := &input.Observations[0]
			observation.Posting.Evidence.ObservedAt = input.Now.Add(-time.Hour - time.Second)
			observation.Posting.Evidence.CheckedAt = input.Now.Add(-time.Minute)
			observation.Target.ObservedAt = input.Now.Add(-time.Hour)
			observation.ReconciledAt = input.Now.Add(-time.Hour + time.Second)
			observation.NativePointAdmittedAt = input.Now.Add(-age)
			observation.NativePointRecordedAt = observation.NativePointAdmittedAt.Add(time.Second)
			observation.NativeStartedAt = observation.NativePointAdmittedAt.Add(time.Millisecond)
			observation.NativeFinishedAt = observation.NativeStartedAt.Add(time.Millisecond)
			for index := range observation.Positions {
				observation.Positions[index].ObservedAt = observation.NativeFinishedAt
			}
			blocked := Assess(input).Blocked
			if blocked != (age >= input.MaxAge) {
				t.Fatalf("native freshness boundary changed: age=%s blocked=%v", age, blocked)
			}
		})
	}
}
