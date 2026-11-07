package orderflow

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestNativeChangesWatermarkOverlapAndBoundedResume(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 7, 8, 0, 0, 123456789, time.UTC)
	first := nativeChangesWindow(now, orders.Checkpoint{}, 3)
	if first.Stream != orders.NativeChangesStream || first.To != now.Add(-2*time.Second).Truncate(time.Millisecond) || first.To.Sub(first.From) != 15*time.Minute {
		t.Fatal("initial fixed window", first)
	}
	cp := orders.Checkpoint{Revision: 1, ConfigRevision: 3, WindowFrom: first.From, WindowTo: first.To, Complete: false, Cursor: "25"}
	resumed := nativeChangesWindow(now.Add(time.Hour), cp, 3)
	if !resumed.From.Equal(first.From) || !resumed.To.Equal(first.To) {
		t.Fatal("mid-page resume changed provider window")
	}
	cp.Complete, cp.Exhausted = true, true
	next := nativeChangesWindow(now.Add(time.Minute), cp, 3)
	if !next.From.Equal(first.To.Add(-5 * time.Minute)) {
		t.Fatal("completed watermark lost overlap")
	}
	bounded := nativeChangesWindow(now.Add(90*24*time.Hour), cp, 3)
	if bounded.To.Sub(bounded.From) != 31*24*time.Hour {
		t.Fatal("unbounded outage catchup")
	}
	rotated := nativeChangesWindow(now.Add(time.Minute), cp, 4)
	if rotated.To.Sub(rotated.From) != 15*time.Minute {
		t.Fatal("new consent reused earlier provider cursor")
	}
}
