package ordermoney

import (
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestProjectLinesRequiresKnownPriceAndCurrency(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		price orders.Field[orders.Money]
		code  string
	}{
		{"missing", orders.Field[orders.Money]{}, "price_unknown"},
		{"null", orders.Field[orders.Money]{Presence: orders.PresenceNull}, "price_unknown"},
		{"empty", orders.Field[orders.Money]{Presence: orders.PresenceEmpty}, "price_unknown"},
		{"invalid presence", orders.Field[orders.Money]{Presence: orders.PresenceValue}, "invalid_price"},
		{"currency missing", orders.Present(orders.Money{Amount: "1"}), "currency_unknown"},
		{"currency unsupported", orders.Present(orders.Money{Amount: "1", Currency: "JPY"}), "currency_unsupported"},
		{"negative", orders.Present(orders.Money{Amount: "-1", Currency: "RUB"}), "invalid_price"},
		{"minor units exceed bound", orders.Present(orders.Money{Amount: orders.Decimal(strings.Repeat("9", 128)), Currency: "RUB"}), "invalid_price"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting, mappings := projectionFixture()
			posting.Lines[0].Price = tc.price
			posting.Financial.Lines = []orders.FinancialLine{{Identity: posting.Lines[0].Identity, Price: orders.Present(orders.Money{Amount: "25", Currency: "RUB"})}}
			assertProjectionBlocked(t, posting, mappings, tc.code)
		})
	}
}

func TestProjectLinesKeepsExplicitZeroPriceAndVAT(t *testing.T) {
	t.Parallel()
	posting, mappings := projectionFixture()
	posting.Lines[0].Price = orders.Present(orders.Money{Amount: "0.00", Currency: "RUB"})
	posting.Lines[0].VAT = orders.Present(orders.Decimal("0"))
	posting.Financial.OrderTotal = orders.Present(orders.Money{Amount: "0", Currency: "RUB"})
	positions, validations := ProjectLines(posting, mappings)
	if len(validations) != 0 || len(positions) != 1 || positions[0].Price.Presence != orders.PresenceZero || positions[0].VAT.Presence != orders.PresenceZero {
		t.Fatalf("explicit zero facts = %#v, %#v", positions, validations)
	}
}

func TestProjectLinesRejectsInvalidQuantity(t *testing.T) {
	t.Parallel()
	for _, quantity := range []orders.Decimal{"0", "-1", "-0", "1e2", "1.", orders.Decimal(strings.Repeat("9", 129))} {
		t.Run(string(quantity), func(t *testing.T) {
			t.Parallel()
			posting, mappings := projectionFixture()
			posting.Lines[0].Quantity = quantity
			assertProjectionBlocked(t, posting, mappings, "invalid_quantity")
		})
	}
}

func TestProjectLinesChecksAggregateHalfUpTotal(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, price, quantity, total string }{
		{"below half", "0.0049", "1", "0"},
		{"at half", "0.005", "1", "0.01"},
		{"next half", "0.015", "1", "0.02"},
		{"no unit rounding", "0.00999", "0.5", "0"},
		{"large exact multiplication", "9007199254740993.125", "2.5", "22517998136852482.81"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting, mappings := projectionFixture()
			posting.Lines[0].Quantity = orders.Decimal(tc.quantity)
			posting.Lines[0].Price = orders.Present(orders.Money{Amount: orders.Decimal(tc.price), Currency: "RUB"})
			posting.Financial.OrderTotal = orders.Present(orders.Money{Amount: orders.Decimal(tc.total), Currency: "RUB"})
			positions, validations := ProjectLines(posting, mappings)
			if len(validations) != 0 || len(positions) != 1 || positions[0].Price.Value.Amount != orders.Decimal(tc.price) {
				t.Fatalf("exact aggregate total check = %#v, %#v", positions, validations)
			}
		})
	}
}

func TestProjectLinesRoundsOnceAfterSummingLines(t *testing.T) {
	t.Parallel()
	posting, mappings := projectionFixture()
	posting.Lines[0].Identity.ProviderLineID = "one"
	posting.Lines[0].Quantity = "1"
	posting.Lines[0].Price = orders.Present(orders.Money{Amount: "0.005", Currency: "RUB"})
	second := posting.Lines[0]
	second.Identity.ProviderLineID = "two"
	posting.Lines = append(posting.Lines, second)
	posting.Financial.OrderTotal = orders.Present(orders.Money{Amount: "0.01", Currency: "RUB"})
	positions, validations := ProjectLines(posting, mappings)
	if len(validations) != 0 || len(positions) != 2 {
		t.Fatalf("sum rounded per line instead of aggregate: %#v, %#v", positions, validations)
	}
	posting.Financial.OrderTotal = orders.Present(orders.Money{Amount: "0.02", Currency: "RUB"})
	assertProjectionBlocked(t, posting, mappings, "order_total_mismatch")
}

func TestProjectLinesBlocksCurrencyMismatchAndTotalOverflow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*orders.Posting)
		code   string
	}{
		{"financial currency", func(p *orders.Posting) { p.Financial.Currency = orders.Present("USD") }, "currency_mismatch"},
		{"reported total currency", func(p *orders.Posting) {
			p.Financial.OrderTotal = orders.Present(orders.Money{Amount: "1", Currency: "EUR"})
		}, "currency_mismatch"},
		{"line currencies", func(p *orders.Posting) {
			second := p.Lines[0]
			second.Identity.ProviderLineID = "two"
			second.Price = orders.Present(orders.Money{Amount: "1", Currency: "USD"})
			p.Lines[0].Identity.ProviderLineID = "one"
			p.Lines = append(p.Lines, second)
		}, "currency_mismatch"},
		{"aggregate overflow", func(p *orders.Posting) {
			p.Lines[0].Quantity = orders.Decimal(strings.Repeat("9", 128))
			p.Lines[0].Price = orders.Present(orders.Money{Amount: "2", Currency: "RUB"})
		}, "total_out_of_range"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting, mappings := projectionFixture()
			tc.change(&posting)
			assertProjectionBlocked(t, posting, mappings, tc.code)
		})
	}
}
