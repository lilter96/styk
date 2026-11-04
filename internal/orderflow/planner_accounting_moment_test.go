package orderflow

import (
	"github.com/lilter96/styk/internal/orders"
	"testing"
	"time"
)

func TestTimeMappingNormalizesOnlyNativeAccountingCopy(t *testing.T) {
	t.Parallel()
	precise := time.Date(2026, 11, 4, 15, 41, 17, 584000000, time.UTC)
	posting := orders.Posting{Times: orders.PostingTimes{CreatedAt: orders.Present(precise), ShipmentAt: orders.Present(precise)}}
	write := orders.CustomerOrderWrite{}
	for _, mapping := range []orders.FieldMapping{{Source: "times.created_at", Target: "moment"}, {Source: "times.shipment_at", Target: "delivery_at"}} {
		if got := applyTimeMapping(&write, posting, mapping); got.Code != "" {
			t.Fatal(got)
		}
	}
	if !write.Moment.IsKnown() || !write.Moment.Value.Equal(precise.Truncate(time.Minute)) || !write.DeliveryAt.IsKnown() || !write.DeliveryAt.Value.Equal(precise.Truncate(time.Minute)) {
		t.Fatal("native accounting time not canonical before intent")
	}
	if !posting.Times.CreatedAt.Value.Equal(precise) || !posting.Times.ShipmentAt.Value.Equal(precise) {
		t.Fatal("provider source timestamps modified")
	}
}
