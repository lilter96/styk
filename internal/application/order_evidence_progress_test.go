package application

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

func TestOrderWindowProgressRequiresContiguousCompleteCoverage(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	p := NewWindowProgress[int](from, to)
	if !p.Valid(from, to) || !p.Split() || len(p.Pending) != 2 {
		t.Fatal("failed to split a valid frozen window")
	}
	if err := p.Add(1); err != nil || !p.Valid(from, to) {
		t.Fatalf("first finished slice broke coverage: %v", err)
	}
	gap := p
	gap.Pending = append([]EvidenceWindow(nil), p.Pending...)
	gap.Pending[0].From = gap.Pending[0].From.Add(time.Millisecond)
	if gap.Valid(from, to) {
		t.Fatal("gap was accepted")
	}
	overlap := p
	overlap.Pending = append([]EvidenceWindow(nil), p.Pending...)
	overlap.Pending[0].From = overlap.Pending[0].From.Add(-time.Millisecond)
	if overlap.Valid(from, to) {
		t.Fatal("overlap was accepted")
	}
	if err := p.Add(2); err != nil || len(p.Pending) != 0 || !p.Valid(from, to) {
		t.Fatalf("completed partition invalid: %v", err)
	}
	if err := p.Add(3); err == nil {
		t.Fatal("added a slice after completion")
	}
	tiny := NewWindowProgress[int](from, from.Add(time.Millisecond))
	if tiny.Split() {
		t.Fatal("sub-millisecond split was accepted")
	}
}

func TestCombineCustomerOrderSlicesDoesNotDuplicateReserves(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	reserve := "2"
	order := domain.CustomerOrderObservation{ID: "ORDER", Account: "account", ObservedAt: from, Positions: []domain.CustomerOrderPosition{{ID: "position", Reserve: &reserve, ObservedAt: from}}}
	makeProgress := func(second domain.CustomerOrderObservation) WindowProgress[domain.CustomerOrderImport] {
		p := NewWindowProgress[domain.CustomerOrderImport](from, to)
		if !p.Split() {
			t.Fatal("split failed")
		}
		for _, o := range []domain.CustomerOrderObservation{order, second} {
			w := p.Pending[0]
			b := domain.CustomerOrderImport{Tenant: "tenant", Account: "account", Since: w.From, Until: w.To, StartedAt: from, FinishedAt: to, Orders: []domain.CustomerOrderObservation{o}}
			if err := p.Add(b); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	duplicate := order
	duplicate.ObservedAt = to
	duplicate.Positions = append([]domain.CustomerOrderPosition(nil), order.Positions...)
	duplicate.Positions[0].ObservedAt = to
	p := makeProgress(duplicate)
	got, err := CombineCustomerOrderSlices(p, "tenant", "account", from, to, from, to)
	if err != nil || len(got.Orders) != 1 || len(got.Orders[0].Positions) != 1 || *got.Orders[0].Positions[0].Reserve != "2" {
		t.Fatalf("duplicate reserve leaked into batch: %+v %v", got, err)
	}
	changed := duplicate
	changed.Positions = append([]domain.CustomerOrderPosition(nil), duplicate.Positions...)
	three := "3"
	changed.Positions[0].Reserve = &three
	if _, err = CombineCustomerOrderSlices(makeProgress(changed), "tenant", "account", from, to, from, to); !errors.Is(err, ErrEvidenceConflict) {
		t.Fatalf("changed reserve was not treated as conflict: %v", err)
	}
	p.Pending = []EvidenceWindow{{From: to, To: to.Add(time.Millisecond)}}
	if _, err = CombineCustomerOrderSlices(p, "tenant", "account", from, to, from, to); err == nil {
		t.Fatal("partial or out-of-window source was sealed")
	}
}

func TestCombinePostingSlicesDeduplicatesButDoesNotHideStatusChanges(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	first := domain.PostingObservation{PostingNumber: "posting", OrderID: "order", Status: "awaiting_packaging", ObservedAt: from, Lines: []domain.PostingLine{{OfferID: "a", Quantity: 2}, {OfferID: "b", Quantity: 1}}}
	makeProgress := func(second domain.PostingObservation) WindowProgress[domain.PostingImport] {
		p := NewWindowProgress[domain.PostingImport](from, to)
		if !p.Split() {
			t.Fatal("split failed")
		}
		for _, o := range []domain.PostingObservation{first, second} {
			w := p.Pending[0]
			b := domain.PostingImport{Tenant: "tenant", Account: "account", Since: w.From, Until: w.To, StartedAt: from, FinishedAt: to, Postings: []domain.PostingObservation{o}}
			if err := p.Add(b); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	duplicate := first
	duplicate.ObservedAt = to
	duplicate.Lines = []domain.PostingLine{first.Lines[1], first.Lines[0]}
	got, err := CombinePostingSlices(makeProgress(duplicate), "tenant", "account", from, to, from, to)
	if err != nil || len(got.Postings) != 1 || len(got.Postings[0].Lines) != 2 || !got.Postings[0].ObservedAt.Equal(to) {
		t.Fatalf("duplicate posting was not collapsed: %+v %v", got, err)
	}
	duplicate.Status = "cancelled"
	if _, err = CombinePostingSlices(makeProgress(duplicate), "tenant", "account", from, to, from, to); !errors.Is(err, ErrEvidenceConflict) {
		t.Fatalf("posting status conflict hidden: %v", err)
	}
}
