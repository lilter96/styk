package orders

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestDiffCommissionSourceSeparatesHeadersAndSortedFinancialRows(t *testing.T) {
	t.Parallel()
	before := RealizationReport{Scope: Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}, Period: RealizationPeriod{Year: 2026, Month: 10}, Number: "R1", Currency: "RUB", Fingerprint: "before", ObservedAt: time.Unix(100, 0), Rows: []RealizationRow{{Number: 9, Delivery: RealizationAmounts{Bonus: Present(Decimal("1"))}}, {Number: 2, Delivery: RealizationAmounts{Bonus: Present(Decimal("2"))}}, {Number: 5, Delivery: RealizationAmounts{Bonus: Present(Decimal("3"))}}}}
	tests := []struct {
		name                    string
		change                  func(*RealizationReport)
		header                  bool
		added, removed, changed []int64
	}{
		{name: "reorder and observation facts", change: func(a *RealizationReport) {
			slices.Reverse(a.Rows)
			a.ObservedAt = time.Unix(200, 0)
			a.Blockers = []Validation{{Code: "review_caveat"}}
		}},
		{name: "header identity", change: func(a *RealizationReport) { a.Number = "R2" }, header: true},
		{name: "financial field", change: func(a *RealizationReport) { a.Rows[0].Delivery.Bonus = Present(Decimal("-3")) }, changed: []int64{9}},
		{name: "added removed changed sorted", change: func(a *RealizationReport) {
			a.Rows = []RealizationRow{{Number: 8}, {Number: 3}, {Number: 5, Delivery: RealizationAmounts{Bonus: Present(Decimal("4"))}}}
		}, added: []int64{3, 8}, removed: []int64{2, 9}, changed: []int64{5}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			after := before
			after.Rows = slices.Clone(before.Rows)
			after.Fingerprint = "after"
			tc.change(&after)
			actual, err := DiffCommissionSource(before, after)
			if err != nil || actual == nil {
				t.Fatal(err)
			}
			expected := CommissionSourceDiff{BeforeFingerprint: "before", AfterFingerprint: "after", HeaderChanged: tc.header, AddedRows: slices.Clone(tc.added), RemovedRows: slices.Clone(tc.removed), ChangedRows: slices.Clone(tc.changed)}
			if len(expected.AddedRows) == 0 {
				expected.AddedRows = []int64{}
			}
			if len(expected.RemovedRows) == 0 {
				expected.RemovedRows = []int64{}
			}
			if len(expected.ChangedRows) == 0 {
				expected.ChangedRows = []int64{}
			}
			if !reflect.DeepEqual(*actual, expected) {
				t.Fatalf("diff=%+v want=%+v", actual, expected)
			}
			if before.Rows[0].Number != 9 || *before.Rows[0].Delivery.Bonus.Value != "1" {
				t.Fatal("comparison mutated frozen source")
			}
		})
	}
}

func TestDiffCommissionSourceRejectsForeignPeriodAndIdentity(t *testing.T) {
	t.Parallel()
	before := RealizationReport{Scope: Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}, Period: RealizationPeriod{Year: 2026, Month: 10}, Fingerprint: "before"}
	tests := []struct {
		name   string
		change func(*RealizationReport)
	}{{"tenant", func(a *RealizationReport) { a.Scope.Tenant = "foreign" }}, {"account", func(a *RealizationReport) { a.Scope.OzonAccount = "foreign" }}, {"period", func(a *RealizationReport) { a.Period.Month = 9 }}, {"missing source version", func(a *RealizationReport) { a.Fingerprint = "" }}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			after := before
			after.Fingerprint = "after"
			tc.change(&after)
			actual, err := DiffCommissionSource(before, after)
			if actual != nil || !errors.Is(err, ErrInvalid) {
				t.Fatalf("diff=%+v err=%v", actual, err)
			}
		})
	}
}
