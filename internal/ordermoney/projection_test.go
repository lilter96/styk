package ordermoney

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func projectionFixture() (orders.Posting, []orders.ProductMappingRef) {
	scope := orders.Scope{Tenant: "tenant-a", OzonAccount: "ozon-a", MoyskladAccount: "moy-a"}
	identity := orders.LineIdentity{SKU: "9007199254740993", OfferID: "offer-a"}
	line := orders.PostingLine{Identity: identity, ProductID: orders.Present("18446744073709551615"), Quantity: "2.5",
		Price: orders.Present(orders.Money{Amount: "1234567890123456789012.3456", Currency: "RUB"})}
	mapping := orders.ProductMappingRef{ID: "mapping-a", Version: 1, Scope: scope, OzonProductID: "18446744073709551615",
		SKU: identity.SKU, OfferID: identity.OfferID, MoyskladKind: "variant", MoyskladID: "variant-a", Confirmed: true, ConfirmedAt: time.Unix(100, 0).UTC()}
	return orders.Posting{Scope: scope, Lines: []orders.PostingLine{line}}, []orders.ProductMappingRef{mapping}
}

func TestProjectLinesPreservesExactFactsAndVariantIdentity(t *testing.T) {
	t.Parallel()
	posting, mappings := projectionFixture()
	positions, validations := ProjectLines(posting, mappings)
	if len(validations) != 0 || len(positions) != 1 {
		t.Fatalf("projection = %#v, %#v", positions, validations)
	}
	position := positions[0]
	if position.Quantity != "2.5" || position.Identity != posting.Lines[0].Identity || position.Mapping != mappings[0] {
		t.Fatalf("exact identity, mapping or quantity changed: %#v", position)
	}
	if position.Mapping.OzonProductID == position.Identity.SKU || position.Mapping.MoyskladKind != "variant" {
		t.Fatal("SKU and product namespaces were conflated or variant kind was lost")
	}
	if !position.Reserve.IsMissing() || !position.VAT.IsMissing() || !reflect.DeepEqual(position.Price, posting.Lines[0].Price) {
		t.Fatal("projection invented reserve/VAT or changed the observed monetary amount")
	}
	position.Price.Value.Amount = "1"
	if posting.Lines[0].Price.Value.Amount != "1234567890123456789012.3456" {
		t.Fatal("projected mutable price aliases the source fact")
	}
}

func TestProjectLinesNeverInfersCancellationArithmeticOrReserve(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"cancelled", "delivered", "awaiting_packaging", "future_status"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			posting, mappings := projectionFixture()
			posting.Status = orders.Present(status)
			posting.Lines[0].Quantity = "5"
			posting.Lines[0].CancelledQuantity = orders.Present(orders.Decimal("2"))
			positions, validations := ProjectLines(posting, mappings)
			if len(validations) != 0 || len(positions) != 1 || positions[0].Quantity != "5" || !positions[0].Reserve.IsMissing() {
				t.Fatalf("cancellation changed projection: %#v, %#v", positions, validations)
			}
			if *posting.Lines[0].CancelledQuantity.Value != "2" {
				t.Fatal("source cancellation quantity changed")
			}
		})
	}
}

func TestProjectLinesRequiresConfirmedMatchingScopedMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*orders.Posting, *[]orders.ProductMappingRef)
		code   string
	}{
		{"missing", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { *m = nil }, "product_mapping_unknown"},
		{"another tenant", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { (*m)[0].Scope.Tenant = "other" }, "product_mapping_unknown"},
		{"another account", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { (*m)[0].Scope.OzonAccount = "other" }, "product_mapping_unknown"},
		{"unconfirmed", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { (*m)[0].Confirmed = false }, "product_mapping_unknown"},
		{"unversioned", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { (*m)[0].Version = 0 }, "product_mapping_unknown"},
		{"wrong SKU", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { (*m)[0].SKU = "other" }, "product_mapping_unknown"},
		{"wrong offer", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { (*m)[0].OfferID = "other" }, "product_mapping_unknown"},
		{"wrong product", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { (*m)[0].OzonProductID = "other" }, "product_mapping_unknown"},
		{"SKU substituted for product", func(p *orders.Posting, m *[]orders.ProductMappingRef) {
			(*m)[0].OzonProductID = p.Lines[0].Identity.SKU
		}, "product_mapping_unknown"},
		{"missing target", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { (*m)[0].MoyskladID = "" }, "product_mapping_unknown"},
		{"unsupported kind", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { (*m)[0].MoyskladKind = "service" }, "product_mapping_unknown"},
		{"multiple confirmed matches", func(_ *orders.Posting, m *[]orders.ProductMappingRef) { *m = append(*m, (*m)[0]) }, "ambiguous_product_mapping"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting, mappings := projectionFixture()
			tc.change(&posting, &mappings)
			assertProjectionBlocked(t, posting, mappings, tc.code)
		})
	}
}

func TestProjectLinesResolvesSKUOrOfferWithoutGuessingProductID(t *testing.T) {
	t.Parallel()
	for _, bySKU := range []bool{true, false} {
		posting, mappings := projectionFixture()
		posting.Lines[0].ProductID = orders.Field[string]{}
		if bySKU {
			posting.Lines[0].Identity.OfferID, mappings[0].OfferID = "", ""
		} else {
			posting.Lines[0].Identity.SKU, mappings[0].SKU = "", ""
		}
		positions, validations := ProjectLines(posting, mappings)
		if len(validations) != 0 || len(positions) != 1 || positions[0].Mapping.OzonProductID != "18446744073709551615" {
			t.Fatalf("mapping without product source evidence = %#v, %#v", positions, validations)
		}
	}
}

func TestProjectLinesRejectsAmbiguousRepeatedLines(t *testing.T) {
	t.Parallel()
	posting, mappings := projectionFixture()
	posting.Lines = append(posting.Lines, posting.Lines[0])
	for range 2 {
		assertProjectionBlocked(t, posting, mappings, "ambiguous_line_identity")
		slices.Reverse(posting.Lines)
	}
	posting.Lines[0].Identity.ProviderLineID = "provider-one"
	posting.Lines[1].Identity.ProviderLineID = "provider-two"
	positions, validations := ProjectLines(posting, mappings)
	if len(validations) != 0 || len(positions) != 2 {
		t.Fatalf("distinct provider line IDs = %#v, %#v", positions, validations)
	}
}

func TestProjectLinesDistinguishesOffersAndIgnoresMappingOrder(t *testing.T) {
	t.Parallel()
	posting, mappings := projectionFixture()
	second := posting.Lines[0]
	second.Identity.OfferID, second.ProductID = "offer-b", orders.Present("product-b")
	posting.Lines = append(posting.Lines, second)
	mapping := mappings[0]
	mapping.ID, mapping.OfferID, mapping.OzonProductID, mapping.MoyskladID = "mapping-b", "offer-b", "product-b", "variant-b"
	mappings = append(mappings, mapping)
	for range 2 {
		positions, validations := ProjectLines(posting, mappings)
		if len(validations) != 0 || len(positions) != 2 || positions[0].Mapping.MoyskladID != "variant-a" || positions[1].Mapping.MoyskladID != "variant-b" {
			t.Fatalf("offer mappings = %#v, %#v", positions, validations)
		}
		slices.Reverse(mappings)
	}
}

func assertProjectionBlocked(t *testing.T, posting orders.Posting, mappings []orders.ProductMappingRef, code string) {
	t.Helper()
	positions, validations := ProjectLines(posting, mappings)
	if len(positions) != 0 || len(validations) == 0 || validations[0].Code != code {
		t.Fatalf("projection = %#v, %#v; want block %q", positions, validations, code)
	}
}
