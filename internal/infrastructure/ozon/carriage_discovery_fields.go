package ozon

import (
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type carriageMethod struct {
	id, warehouse, date string
	ready               int
	candidates          []string
	blockers            []orders.Validation
}

// Field identities and types are retained from the authenticated v2 response.
// Descriptions and addresses are deliberately excluded from persisted evidence.
func decodeCarriageMethod(f orderFields) (carriageMethod, error) {
	m := carriageMethod{id: orderKnown(f.id("delivery_method_id")), warehouse: orderKnown(f.id("warehouse_id")), date: orderKnown(f.text("departure_date")), candidates: []string{}, blockers: []orders.Validation{}}
	if !orders.CarriageIDValid(m.id) || !orders.CarriageIDValid(m.warehouse) {
		return m, orders.ErrSchemaDrift
	}
	if _, err := time.Parse("2006-01-02", m.date); err != nil {
		return m, orders.ErrSchemaDrift
	}
	if err := carriageMethodProfile(f, &m); err != nil {
		return m, err
	}
	for _, key := range []string{"mandatory_packaged_count", "optional_packaged_count"} {
		count, err := strconv.ParseInt(string(f.raw[key]), 10, 32)
		if err != nil || count < 0 {
			return m, orders.ErrSchemaDrift
		}
		m.ready += int(count)
	}
	if !carriageArrayPresent(f, "carriages") || !carriageArrayPresent(f, "errors") {
		return m, orders.ErrSchemaDrift
	}
	if err := carriageMethodCarriages(f, &m); err != nil {
		return m, err
	}
	if err := carriageMethodErrors(f, &m); err != nil {
		return m, err
	}
	if *f.err != nil {
		return m, orders.ErrSchemaDrift
	}
	return m, nil
}

func carriageArrayPresent(f orderFields, key string) bool {
	return f.raw[key] != nil && string(f.raw[key]) != "null"
}

func carriageMethodProfile(f orderFields, m *carriageMethod) error {
	rfbs, changing := f.boolean("is_rfbs"), f.boolean("first_mile_changing")
	if !rfbs.IsKnown() || !changing.IsKnown() {
		return orders.ErrSchemaDrift
	}
	if *rfbs.Value || *changing.Value || orderKnown(f.text("delivery_method_status")) != "active" || orderKnown(f.text("first_mile_type")) != "dropoff" || orderKnown(f.text("integration_type")) != "ozon" {
		m.blockers = append(m.blockers, orders.Validation{Code: "CARRIAGE_DELIVERY_PROFILE_UNSUPPORTED"})
	}
	return nil
}

func carriageMethodCarriages(f orderFields, m *carriageMethod) error {
	for _, row := range f.array("carriages") {
		id, status := orderKnown(row.id("id")), orderKnown(row.text("status"))
		if !orders.CarriageIDValid(id) || status == "" {
			return orders.ErrSchemaDrift
		}
		if status != "closed" && status != "cancelled" {
			m.candidates = append(m.candidates, id)
		}
	}
	return nil
}

func carriageMethodErrors(f orderFields, m *carriageMethod) error {
	for _, row := range f.array("errors") {
		severity, code := orderKnown(row.text("status")), orderKnown(row.text("code"))
		if code == "" || (severity != "critical" && severity != "warning") {
			return orders.ErrSchemaDrift
		}
		if severity == "critical" {
			m.blockers = append(m.blockers, orders.Validation{Field: code, Code: "CARRIAGE_PROVIDER_BLOCKED"})
		}
	}
	return nil
}
