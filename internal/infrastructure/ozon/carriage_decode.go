package ozon

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

func jsonCarriageID(id string) json.Number { return json.Number(id) }
func stringsCompare(a, b string) int       { return strings.Compare(a, b) }

func decodeCarriageHeader(data []byte) (orders.CarriageHeader, error) {
	if ordercontract.CheckJSON(data) != nil {
		return orders.CarriageHeader{}, orders.ErrSchemaDrift
	}
	f := newOrderFields(data)
	h := orders.CarriageHeader{CarriageID: orderKnown(f.id("carriage_id")), CompanyID: orderKnown(f.id("company_id")), DeliveryMethodID: orderKnown(f.id("delivery_method_id")), WarehouseID: orderKnown(f.id("warehouse_id")), Status: orderKnown(f.text("status")), AvailableActions: f.strings("available_actions"), DepartureDate: orderKnown(f.text("departure_date")), FirstMileType: orderKnown(f.text("first_mile_type")), IntegrationType: orderKnown(f.text("integration_type"))}
	created, updated := f.timestamp("created_at"), f.timestamp("updated_at")
	if created.IsKnown() {
		h.CreatedAt = *created.Value
	}
	if updated.IsKnown() {
		h.UpdatedAt = *updated.Value
	}
	if raw := f.raw["containers_count"]; raw != nil && string(raw) != "null" {
		value, err := strconv.ParseInt(string(raw), 10, 32)
		if err != nil || value < 0 || strconv.FormatInt(value, 10) != string(raw) {
			return h, orders.ErrSchemaDrift
		}
		count := int32(value)
		h.ContainersCount = &count
	}
	if *f.err != nil || !orders.CarriageIDValid(h.CarriageID) || !orders.CarriageIDValid(h.CompanyID) || !orders.CarriageIDValid(h.DeliveryMethodID) || !orders.CarriageIDValid(h.WarehouseID) || h.Status == "" || h.CreatedAt.IsZero() || h.UpdatedAt.Before(h.CreatedAt) || f.raw["available_actions"] == nil || string(f.raw["available_actions"]) == "null" {
		return h, orders.ErrSchemaDrift
	}
	return h, nil
}

func decodeCarriageMember(f orderFields) (orders.CarriageMember, error) {
	member := orders.CarriageMember{PostingNumber: orderKnown(f.text("posting_number")), Status: orderKnown(f.text("status")), Lines: make([]orders.PostingLine, 0), Blockers: []orders.Validation{}}
	if member.PostingNumber == "" || member.Status == "" || f.raw["products"] == nil || string(f.raw["products"]) == "null" || orderKnown(f.text("seller_error")) != "" {
		return member, orders.ErrSchemaDrift
	}
	seen := make(map[orders.LineIdentity]bool)
	for _, row := range f.array("products") {
		identity := orders.LineIdentity{SKU: orderKnown(row.id("sku")), OfferID: orderKnown(row.text("offer_id"))}
		quantity := row.decimal("quantity")
		if !orders.CarriageIDValid(identity.SKU) || identity.OfferID == "" || !quantity.IsKnown() || seen[identity] {
			return member, orders.ErrSchemaDrift
		}
		qty, err := strconv.ParseInt(string(*quantity.Value), 10, 32)
		if err != nil || qty <= 0 {
			return member, orders.ErrSchemaDrift
		}
		seen[identity] = true
		member.Lines = append(member.Lines, orders.PostingLine{Identity: identity, Quantity: *quantity.Value})
	}
	if *f.err != nil || len(member.Lines) == 0 {
		return member, orders.ErrSchemaDrift
	}
	return member, nil
}

func carriageLinesEqual(a, b []orders.PostingLine) bool {
	project := func(lines []orders.PostingLine) []string {
		result := make([]string, 0, len(lines))
		for _, line := range lines {
			result = append(result, line.Identity.SKU+"\x00"+line.Identity.OfferID+"\x00"+string(line.Quantity))
		}
		slices.Sort(result)
		return result
	}
	return reflect.DeepEqual(project(a), project(b))
}
