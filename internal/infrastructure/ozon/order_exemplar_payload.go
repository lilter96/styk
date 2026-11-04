package ozon

import "github.com/lilter96/styk/internal/orders"

func exemplarSetPayload(command orders.Command) (map[string]any, error) {
	input := command.PostingAction.Parameters.Exemplars
	if input == nil || len(input.Products) == 0 || len(input.Products) > 1000 {
		return nil, orders.ErrInvalid
	}
	products := make([]map[string]any, 0, len(input.Products))
	for _, p := range input.Products {
		sku, err := parseSKU(p.SKU)
		if err != nil || len(p.Exemplars) == 0 || len(p.Exemplars) > 1000 {
			return nil, orders.ErrInvalid
		}
		units := make([]map[string]any, 0, len(p.Exemplars))
		for _, u := range p.Exemplars {
			v, e := exemplarUnitPayload(u)
			if e != nil {
				return nil, e
			}
			units = append(units, v)
		}
		products = append(products, map[string]any{"product_id": sku, "exemplars": units})
	}
	return map[string]any{"posting_number": command.PostingNumber, "products": products}, nil
}

func exemplarUnitPayload(u orders.ExemplarInput) (map[string]any, error) {
	if u.Validate() != nil || len(u.ClearMarkTypes) > 0 {
		return nil, orders.ErrInvalid
	}
	id, err := parseSKU(u.ID)
	if err != nil {
		return nil, orders.ErrInvalid
	}
	value := map[string]any{"exemplar_id": id}
	if u.GTD.IsKnown() {
		value["gtd"] = *u.GTD.Value
	}
	if u.GTD.Presence == orders.PresenceEmpty {
		value["gtd"] = ""
	}
	if u.GTDAbsent.IsKnown() {
		value["is_gtd_absent"] = *u.GTDAbsent.Value
	}
	if u.RNPT.IsKnown() {
		value["rnpt"] = *u.RNPT.Value
	}
	if u.RNPT.Presence == orders.PresenceEmpty {
		value["rnpt"] = ""
	}
	if u.RNPTAbsent.IsKnown() {
		value["is_rnpt_absent"] = *u.RNPTAbsent.Value
	}
	if u.Marks.Presence == orders.PresenceEmpty {
		value["marks"] = []any{}
	}
	if u.Marks.IsKnown() {
		marks := make([]map[string]string, 0, len(*u.Marks.Value))
		for _, m := range *u.Marks.Value {
			marks = append(marks, map[string]string{"mark_type": m.Type, "mark": m.Value})
		}
		value["marks"] = marks
	}
	return value, nil
}
