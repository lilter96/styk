package orders

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func nativePointFixture(t *testing.T) (TargetObservation, time.Time, time.Time) {
	t.Helper()
	command, baseline := positionPatchFixture(t)
	at := time.Unix(200, 123456789).UTC()
	target := TargetObservation{Scope: command.Scope, Correlation: baseline.Correlation, Revision: "native-exact-revision", Complete: true, ObservedAt: at, Fields: *command.CustomerOrder, Positions: slices.Clone(command.TargetPositions), ProviderUpdatedAt: Present("2026-11-04 15:00:00.000")}
	target.Fields.Applicable = Present(true)
	target.Fields.Moment = Present(time.Unix(120, 0).UTC())
	return target, at.Add(-time.Second), at.Add(time.Second)
}

func TestNativeOrderPointPreservesExactUnknownNullAndZero(t *testing.T) {
	t.Parallel()
	target, start, end := nativePointFixture(t)
	target.Positions[0].Reserve = Field[Decimal]{}
	target.Positions[0].Shipped = Field[Decimal]{Presence: PresenceNull}
	target.Positions[0].Discount = Present(Decimal("0"))
	point, err := NewNativeOrderPoint(target, 7, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !point.Positions[0].Reserve.IsMissing() || point.Positions[0].Shipped.Presence != PresenceNull || point.Positions[0].Discount.Presence != PresenceZero {
		t.Fatal("unknown, null and explicitzero collapsed")
	}
	if point.CredentialRevision != 7 || !point.StartedAt.Equal(start) || !point.FinishedAt.Equal(end) || !point.ObservedAt.Equal(target.ObservedAt) || point.ObservedAt.Nanosecond() != 123456789 || *point.ProviderUpdatedAt.Value != *target.ProviderUpdatedAt.Value {
		t.Fatal("actual clocks or native timezone-less fact rewritten")
	}
	raw, err := json.Marshal(point)
	if err != nil {
		t.Fatal(err)
	}
	var decoded NativeOrderPoint
	if err = json.Unmarshal(raw, &decoded); err != nil || decoded.Validate() != nil {
		t.Fatal("point roundtrip invalid", err)
	}
	if !decoded.Positions[0].Reserve.IsMissing() || decoded.Positions[0].Shipped.Presence != PresenceNull || !decoded.Positions[0].Discount.IsKnown() {
		t.Fatal("serialized unknown facts became zero")
	}
}

func TestNativeOrderPointCopiesOnlyBoundedReadSideFacts(t *testing.T) {
	t.Parallel()
	target, start, end := nativePointFixture(t)
	target.Fields.Name, target.Fields.Description = Present("private-marker"), Present("private-marker")
	target.Fields.ShipmentAddress = Present("private-marker")
	target.Fields.Attributes = []AttributeWrite{{AttributeID: "private-attribute", Value: Present("private-marker")}}
	point, err := NewNativeOrderPoint(target, 1, start, end)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(point)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{"private-marker", "private-attribute", "shipment_address", "attributes", "description", `"customer":`} {
		if strings.Contains(string(raw), sensitive) {
			t.Fatalf("unnecessary data retained: %s", sensitive)
		}
	}
	target.Positions[0].ID = "changed-source"
	*target.Positions[0].Reserve.Value = "3"
	*target.Fields.Applicable.Value = false
	*target.ProviderUpdatedAt.Value = "changed-provider-time"
	if point.Positions[0].ID == target.Positions[0].ID || *point.Positions[0].Reserve.Value != "0" || !*point.Applicable.Value || *point.ProviderUpdatedAt.Value == *target.ProviderUpdatedAt.Value {
		t.Fatal("point aliases mutable input observations")
	}
}

type nativePointMutation struct {
	name  string
	alter func(*TargetObservation, *int64, *time.Time, *time.Time)
}

func TestNativeOrderPointRejectsPartialOrSubstitutedFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range nativePointInvalidCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target, start, end := nativePointFixture(t)
			revision := int64(1)
			tc.alter(&target, &revision, &start, &end)
			if _, err := NewNativeOrderPoint(target, revision, start, end); err == nil {
				t.Fatal("invalid exact-target point accepted")
			}
		})
	}
}

func nativePointInvalidCases() []nativePointMutation {
	return []nativePointMutation{
		{"partial positions", func(p *TargetObservation, _ *int64, _ *time.Time, _ *time.Time) { p.Complete = false }},
		{"cross account", func(p *TargetObservation, _ *int64, _ *time.Time, _ *time.Time) {
			p.Correlation.Scope.MoyskladAccount = "other"
		}},
		{"missing exact native ID", func(p *TargetObservation, _ *int64, _ *time.Time, _ *time.Time) { p.Correlation.CustomerOrderID = "" }},
		{"substituted marker", func(p *TargetObservation, _ *int64, _ *time.Time, _ *time.Time) { p.Correlation.Marker = "other" }},
		{"missing credential revision", func(_ *TargetObservation, r *int64, _ *time.Time, _ *time.Time) { *r = 0 }},
		{"negative credential revision", func(_ *TargetObservation, r *int64, _ *time.Time, _ *time.Time) { *r = -1 }},
		{"missing target revision", func(p *TargetObservation, _ *int64, _ *time.Time, _ *time.Time) { p.Revision = "" }},
		{"unknown store", func(p *TargetObservation, _ *int64, _ *time.Time, _ *time.Time) { p.Fields.StoreID = "" }},
		{"missing actual observation", func(p *TargetObservation, _ *int64, _ *time.Time, _ *time.Time) { p.ObservedAt = time.Time{} }},
		{"read began after observation", func(_ *TargetObservation, _ *int64, s *time.Time, e *time.Time) { *s = *e }},
		{"read finished before observation", func(_ *TargetObservation, _ *int64, s *time.Time, e *time.Time) { *e = *s }},
		{"missing read start", func(_ *TargetObservation, _ *int64, s *time.Time, _ *time.Time) { *s = time.Time{} }},
		{"duplicate native UUID", func(p *TargetObservation, _ *int64, _ *time.Time, _ *time.Time) {
			p.Positions = append(p.Positions, p.Positions[0])
		}},
		{"negative reserve", func(p *TargetObservation, _ *int64, _ *time.Time, _ *time.Time) {
			p.Positions[0].Reserve = Present(Decimal("-1"))
		}},
		{"malformed applicable", func(p *TargetObservation, _ *int64, _ *time.Time, _ *time.Time) {
			p.Fields.Applicable = Field[bool]{Presence: PresenceValue}
		}},
	}
}

func TestNativeOrderPointBoundsEnumerationWithoutClaimingAccountCoverage(t *testing.T) {
	t.Parallel()
	target, start, end := nativePointFixture(t)
	target.Positions = nil
	point, err := NewNativeOrderPoint(target, 1, start, end)
	if err != nil || point.Validate() != nil || len(point.Positions) != 0 {
		t.Fatal("actual complete empty enumeration became unknown or invalid", err)
	}
	target.Positions = make([]TargetPosition, MaxNativeOrderPointPositions+1)
	if _, err = NewNativeOrderPoint(target, 1, start, end); err == nil {
		t.Fatal("unbounded position enumeration retained")
	}
}
