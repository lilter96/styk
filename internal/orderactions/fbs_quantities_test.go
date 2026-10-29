package orderactions

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestFBSPackagesConserveActualQuantities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		quantities []orders.Decimal
		want       error
	}{
		{name: "one package", quantities: []orders.Decimal{"3"}},
		{name: "split across packages", quantities: []orders.Decimal{"1", "2"}},
		{name: "omitted units", quantities: []orders.Decimal{"2"}, want: orders.ErrBlocked},
		{name: "over allocation", quantities: []orders.Decimal{"2", "2"}, want: orders.ErrConflict},
		{name: "zero", quantities: []orders.Decimal{"0"}, want: orders.ErrInvalid},
		{name: "fraction", quantities: []orders.Decimal{"1.5"}, want: orders.ErrInvalid},
		{name: "overflow", quantities: []orders.Decimal{"18446744073709551616"}, want: orders.ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting, _, _ := fbsFixture(t)
			packages := make([]FBSPackage, 0, len(tc.quantities))
			for _, quantity := range tc.quantities {
				packages = append(packages, FBSPackage{Lines: []FBSQuantity{{Identity: posting.Lines[0].Identity, Quantity: quantity}}})
			}
			if err := ValidateFBSPackages(posting, packages); !errors.Is(err, tc.want) {
				t.Fatalf("quantities %v: got %v want %v", tc.quantities, err, tc.want)
			}
		})
	}
}

func TestFBSPackagesDoNotGuessMissingOrCancelledUnits(t *testing.T) {
	t.Parallel()
	posting, _, _ := fbsFixture(t)
	packages := []FBSPackage{{Lines: []FBSQuantity{{Identity: posting.Lines[0].Identity, Quantity: "2"}}}}
	posting.Lines[0].CancelledQuantity = orders.Present(orders.Decimal("1"))
	if err := ValidateFBSPackages(posting, packages); err != nil {
		t.Fatal(err)
	}
	posting.Lines[0].CancelledQuantity = orders.Field[orders.Decimal]{}
	if err := ValidateFBSPackages(posting, packages); !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("absent cancelled count became zero: %v", err)
	}
	posting.Lines[0].CancelledQuantity = orders.Present(orders.Decimal("4"))
	if err := ValidateFBSPackages(posting, packages); !errors.Is(err, orders.ErrConflict) {
		t.Fatalf("negative remaining units accepted: %v", err)
	}
}

func TestFBSPartialCancellationRequiresReasonAndQuantities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*FBSPartialCancellation)
		want   error
	}{
		{name: "valid", change: func(*FBSPartialCancellation) {}},
		{name: "reason missing", change: func(i *FBSPartialCancellation) { i.ReasonID = "" }, want: orders.ErrInvalid},
		{name: "reason not offered", change: func(i *FBSPartialCancellation) { i.ReasonID = "999" }, want: orders.ErrInvalid},
		{name: "quantity missing", change: func(i *FBSPartialCancellation) { i.Lines[0].Quantity = "" }, want: orders.ErrInvalid},
		{name: "zero", change: func(i *FBSPartialCancellation) { i.Lines[0].Quantity = "0" }, want: orders.ErrInvalid},
		{name: "complete cancellation", change: func(i *FBSPartialCancellation) { i.Lines[0].Quantity = "3" }, want: orders.ErrConflict},
		{name: "stale catalogue", change: func(i *FBSPartialCancellation) { i.Catalogue.SourceRevision = "source-0" }, want: orders.ErrStale},
		{name: "cross tenant catalogue", change: func(i *FBSPartialCancellation) { i.Catalogue.Scope.Tenant = "other" }, want: orders.ErrStale},
		{name: "expired catalogue", change: func(i *FBSPartialCancellation) { i.Catalogue.ValidUntil = time.Unix(0, 0) }, want: orders.ErrStale},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting, _, now := fbsFixture(t)
			input := fbsCancellationFixture(posting, now)
			tc.change(&input)
			if err := ValidateFBSPartialCancellation(posting, input, now); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func fbsCancellationFixture(posting orders.Posting, now time.Time) FBSPartialCancellation {
	reason := "900719925474099312348"
	return FBSPartialCancellation{ReasonID: reason, Catalogue: FBSReasonCatalogue{Scope: posting.Scope, PostingNumber: posting.PostingNumber,
		SourceRevision: posting.Evidence.Revision, ReasonIDs: []string{reason}, ObservedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute)},
		Lines: []FBSQuantity{{Identity: posting.Lines[0].Identity, Quantity: "1"}}}
}

func TestFBSReservationFrontierNeverIncludesParent(t *testing.T) {
	t.Parallel()
	parent, children := fbsSplitFixture(t)
	numbers, err := FBSReservationFrontier(parent, children)
	if err != nil || !slices.Equal(numbers, parent.ChildPostingNumbers) || slices.Contains(numbers, parent.PostingNumber) {
		t.Fatalf("incorrect reservation frontier: %v %v", numbers, err)
	}
	children[1].Lines[0].Quantity = "3"
	if _, err := FBSReservationFrontier(parent, children); !errors.Is(err, orders.ErrConflict) {
		t.Fatalf("parent units duplicated in children: %v", err)
	}
}

func TestFBSSplitRequiresCompleteScopedLineage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*orders.Posting, []orders.Posting)
	}{
		{name: "unlinked", change: func(_ *orders.Posting, c []orders.Posting) { c[1].ParentPostingNumber = orders.Field[string]{} }},
		{name: "cross tenant", change: func(_ *orders.Posting, c []orders.Posting) { c[1].Scope.Tenant = "other" }},
		{name: "unknown children", change: func(p *orders.Posting, _ []orders.Posting) { p.ChildPostingNumbers = nil }},
		{name: "nested split", change: func(_ *orders.Posting, c []orders.Posting) {
			c[0].ChildPostingNumbers = []string{"unobserved-grandchild"}
		}},
		{name: "duplicate child", change: func(_ *orders.Posting, c []orders.Posting) { c[1] = c[0] }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parent, children := fbsSplitFixture(t)
			tc.change(&parent, children)
			if numbers, err := FBSReservationFrontier(parent, children); err == nil || len(numbers) != 0 {
				t.Fatalf("unreconciled split accepted: %v %v", numbers, err)
			}
		})
	}
}

func fbsSplitFixture(t *testing.T) (orders.Posting, []orders.Posting) {
	t.Helper()
	parent, _, _ := fbsFixture(t)
	parent.ChildPostingNumbers = []string{"TEST-FBS-0001-2", "TEST-FBS-0001-3"}
	children := make([]orders.Posting, 0, 2)
	for index, number := range parent.ChildPostingNumbers {
		child := parent
		child.PostingNumber, child.ParentPostingNumber, child.ChildPostingNumbers = number, orders.Present(parent.PostingNumber), nil
		child.Lines = slices.Clone(parent.Lines)
		child.Lines[0].Quantity = orders.Decimal([]string{"1", "2"}[index])
		children = append(children, child)
	}
	return parent, children
}
