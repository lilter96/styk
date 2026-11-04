package postgres

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func nativeInventoryPointFixture(t *testing.T) (nativeInventoryPointRow, orders.NativeOrderPoint) {
	t.Helper()
	scope := orders.Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}
	correlation, err := orders.CorrelationFor(scope, "posting")
	if err != nil {
		t.Fatal(err)
	}
	correlation.CustomerOrderID = "native-order"
	now := time.Unix(1700000000, 123456000).UTC()
	point := orders.NativeOrderPoint{Scope: scope, Correlation: correlation, CredentialRevision: 1, StartedAt: now, FinishedAt: now.Add(time.Second), ObservedAt: now.Add(time.Second), TargetRevision: "revision", StoreID: "store", ProviderUpdatedAt: orders.Present("2026-11-04 18:00:00.123"), Positions: []orders.TargetPosition{{ID: "position", MoyskladKind: "product", MoyskladID: "item", Quantity: "2", Shipped: orders.Field[orders.Decimal]{Presence: orders.PresenceNull}, Discount: orders.Present(orders.Decimal("0"))}}}
	if err = point.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(point)
	if err != nil {
		t.Fatal(err)
	}
	facts := point
	facts.StartedAt, facts.FinishedAt, facts.ObservedAt = time.Time{}, time.Time{}, time.Time{}
	facts.CredentialRevision = 0
	hash, err := ordersHash(facts)
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(24 * time.Hour)
	observed := at.Add(time.Second)
	row := nativeInventoryPointRow{raw: raw, syncID: correlation.SyncID, marker: correlation.Marker, targetRevision: point.TargetRevision, factsHash: hash, credentialRevision: 2, currentCredentialRevision: 2, credentialEnabled: true, bindingMatches: true, startedAt: at, finishedAt: at.Add(2 * time.Second), observedAt: &observed, admittedAt: at.Add(-time.Second), recordedAt: at.Add(3 * time.Second)}
	return row, point
}

func TestInventoryNativePointRehydratesActualAttemptWithoutRewritingSnapshot(t *testing.T) {
	t.Parallel()
	row, original := nativeInventoryPointFixture(t)
	before := bytes.Clone(row.raw)
	read := decodeInventoryNativePoint(row, original.Correlation)
	if read.Code != "" || read.Point == nil {
		t.Fatalf("valid read unavailable: %s", read.Code)
	}
	p := read.Point
	if !p.StartedAt.Equal(row.startedAt) || !p.FinishedAt.Equal(row.finishedAt) || !p.ObservedAt.Equal(*row.observedAt) || p.CredentialRevision != 2 || !read.AdmittedAt.Equal(row.admittedAt) || !read.RecordedAt.Equal(row.recordedAt) {
		t.Fatal("actual attempt clocks lost")
	}
	if !bytes.Equal(before, row.raw) || original.CredentialRevision != 1 || original.StartedAt.Equal(p.StartedAt) {
		t.Fatal("immutable snapshot rewritten")
	}
	position := p.Positions[0]
	if !position.Reserve.IsMissing() || position.Shipped.Presence != orders.PresenceNull || position.Discount.Presence != orders.PresenceZero {
		t.Fatal("unknown/null/zero collapsed")
	}
	if !p.ProviderUpdatedAt.IsKnown() || *p.ProviderUpdatedAt.Value != "2026-11-04 18:00:00.123" {
		t.Fatal("native timezone-less update rewritten")
	}
}

func TestInventoryNativePointRejectsInvalidTargetProofIndependently(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, code string
		mutate     func(*nativeInventoryPointRow)
	}{
		{"changed binding", "NATIVE_BINDING_CHANGED", func(r *nativeInventoryPointRow) { r.bindingMatches = false }},
		{"different marker", "NATIVE_BINDING_CHANGED", func(r *nativeInventoryPointRow) { r.marker = "other" }},
		{"rotated credential", "NATIVE_CREDENTIAL_REVISION_CHANGED", func(r *nativeInventoryPointRow) { r.currentCredentialRevision++ }},
		{"disabled credential", "NATIVE_CREDENTIAL_REVISION_CHANGED", func(r *nativeInventoryPointRow) { r.credentialEnabled = false }},
		{"failure never reuses success", "NATIVE_RATE_LIMITED", func(r *nativeInventoryPointRow) { r.code = "NATIVE_RATE_LIMITED"; r.raw = nil; r.observedAt = nil }},
		{"failed row with prior snapshot", "NATIVE_READ_FAILED", func(r *nativeInventoryPointRow) { r.code = "NATIVE_READ_FAILED" }},
		{"missing joined snapshot", "NATIVE_ORDER_SCHEMA_DRIFT", func(r *nativeInventoryPointRow) { r.raw = nil }},
		{"altered facts", "NATIVE_ORDER_SCHEMA_DRIFT", func(r *nativeInventoryPointRow) { r.factsHash = "wrong" }},
		{"wrong snapshot scope", "NATIVE_ORDER_SCHEMA_DRIFT", func(r *nativeInventoryPointRow) {
			r.raw = bytes.ReplaceAll(r.raw, []byte(`"tenant":"tenant"`), []byte(`"tenant":"other"`))
		}},
		{"wrong native ID", "NATIVE_ORDER_SCHEMA_DRIFT", func(r *nativeInventoryPointRow) {
			r.raw = bytes.ReplaceAll(r.raw, []byte(`"native-order"`), []byte(`"other-order"`))
		}},
		{"unknown JSON field", "NATIVE_ORDER_SCHEMA_DRIFT", func(r *nativeInventoryPointRow) { r.raw = append([]byte(`{"unexpected":1,`), r.raw[1:]...) }},
		{"invalid read interval", "NATIVE_ORDER_SCHEMA_DRIFT", func(r *nativeInventoryPointRow) { r.finishedAt = r.startedAt.Add(-time.Second) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			row, point := nativeInventoryPointFixture(t)
			tc.mutate(&row)
			read := decodeInventoryNativePoint(row, point.Correlation)
			if read.Point != nil || read.Code != tc.code {
				t.Fatalf("got point=%v code=%s", read.Point != nil, read.Code)
			}
		})
	}
}
