package orders

import (
	"testing"
	"time"
)

func TestAccountingMomentCompatibilityRejectsArbitraryNearbyTimes(t *testing.T) {
	t.Parallel()
	wanted := time.Date(2026, 11, 4, 15, 41, 17, 584000000, time.UTC)
	for _, tc := range []struct {
		name              string
		actual            time.Time
		allow, normalized bool
	}{
		{"exact", wanted, true, false},
		{"documented_floor", wanted.Truncate(time.Minute), true, true},
		{"previous_minute", wanted.Truncate(time.Minute).Add(-time.Minute), false, false},
		{"next_minute", wanted.Truncate(time.Minute).Add(time.Minute), false, false},
		{"same_minute_arbitrary_second", wanted.Truncate(time.Minute).Add(time.Second), false, false},
		{"floor_plus_millisecond", wanted.Truncate(time.Minute).Add(time.Millisecond), false, false},
		{"time_zone_same_instant", wanted.In(time.FixedZone("MSK", 3*60*60)), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := AccountingMomentMatches(Present(wanted), Present(tc.actual)); got != tc.allow {
				t.Fatalf("match=%v", got)
			}
			if got := AccountingMomentNormalized(Present(wanted), Present(tc.actual)); got != tc.normalized {
				t.Fatalf("normalized=%v", got)
			}
		})
	}
	if AccountingMomentMatches(Present(wanted), Field[time.Time]{}) {
		t.Fatal("missing moment treated as zero/floor")
	}
}

func TestNativeMinuteReadbackDoesNotRewriteLegacyIntentOrEvent(t *testing.T) {
	t.Parallel()
	command, _ := nativeDomainFixture(t)
	command.NativeMovement.Moment = time.Date(2026, 11, 4, 15, 41, 17, 584000000, time.UTC)
	event := command.NativeMovement.Event.OccurredAt
	before, err := CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	actual := NativeDocumentObservation{Moment: Present(command.NativeMovement.Moment.Truncate(time.Minute))}
	if !NativeDocumentMomentNormalized(command, actual) {
		t.Fatal("legacy documented minute precision not diagnosed")
	}
	after, err := CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || !command.NativeMovement.Event.OccurredAt.Equal(event) || command.NativeMovement.Moment.Second() != 17 {
		t.Fatal("immutable intent or precise event rewritten")
	}
}
