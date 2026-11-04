package ozon

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/lilter96/styk/internal/orders"
)

const exemplarPreparePath = "/v6/fbs/posting/product/exemplar/create-or-get"
const exemplarSetPath = "/v6/fbs/posting/product/exemplar/set"
const exemplarStatusPath = "/v5/fbs/posting/product/exemplar/status"

type exemplarWire struct {
	ID           json.RawMessage `json:"exemplar_id"`
	GTD          json.RawMessage `json:"gtd"`
	GTDAbsent    json.RawMessage `json:"is_gtd_absent"`
	RNPT         json.RawMessage `json:"rnpt"`
	RNPTAbsent   json.RawMessage `json:"is_rnpt_absent"`
	Marks        json.RawMessage `json:"marks"`
	Weight       json.RawMessage `json:"weight"`
	GTDCheck     string          `json:"gtd_check_status"`
	GTDErrors    []string        `json:"gtd_error_codes"`
	RNPTCheck    string          `json:"rnpt_check_status"`
	RNPTErrors   []string        `json:"rnpt_error_codes"`
	WeightCheck  string          `json:"weight_check_status"`
	WeightErrors []string        `json:"weight_error_codes"`
}
type exemplarProductWire struct {
	ID           json.RawMessage `json:"product_id"`
	Quantity     int64           `json:"quantity"`
	Exemplars    []exemplarWire  `json:"exemplars"`
	GTD          json.RawMessage `json:"is_gtd_needed"`
	RNPT         json.RawMessage `json:"is_rnpt_needed"`
	Mark         json.RawMessage `json:"is_mandatory_mark_needed"`
	MarkPossible json.RawMessage `json:"is_mandatory_mark_possible"`
	Jewelry      json.RawMessage `json:"is_jw_uin_needed"`
	IMEI         json.RawMessage `json:"has_imei"`
	Weight       json.RawMessage `json:"is_weight_needed"`
	WeightMin    json.RawMessage `json:"weight_min"`
	WeightMax    json.RawMessage `json:"weight_max"`
}
type exemplarResponseWire struct {
	Posting  string                `json:"posting_number"`
	Products []exemplarProductWire `json:"products"`
	Status   string                `json:"status"`
	MultiBox json.RawMessage       `json:"multi_box_qty"`
}
type exemplarMarkWire struct {
	Value  string   `json:"mark"`
	Type   string   `json:"mark_type"`
	Status string   `json:"check_status"`
	Errors []string `json:"error_codes"`
}

func decodeExemplarSnapshot(data []byte, posting string) (orders.ExemplarSnapshot, error) {
	var response exemplarResponseWire
	if decodeLabelJSON(data, &response) != nil || response.Posting != posting || len(response.Products) == 0 || len(response.Products) > 1000 {
		return orders.ExemplarSnapshot{}, orders.ErrSchemaDrift
	}
	s := orders.ExemplarSnapshot{PostingNumber: posting, Products: make([]orders.ExemplarProduct, 0, len(response.Products))}
	for _, row := range response.Products {
		p, e := decodeExemplarProduct(row)
		if e != nil {
			return s, e
		}
		s.Products = append(s.Products, p)
	}
	return s, nil
}

func decodeExemplarProduct(w exemplarProductWire) (orders.ExemplarProduct, error) {
	sku, err := orderID(w.ID)
	if err != nil || len(w.Exemplars) > 1000 {
		return orders.ExemplarProduct{}, orders.ErrInvalid
	}
	p := orders.ExemplarProduct{SKU: sku, Quantity: w.Quantity, Required: make(map[orders.PackingRequirementKind]orders.Field[bool]), Exemplars: make([]orders.ExemplarInput, 0, len(w.Exemplars))}
	for kind, raw := range map[orders.PackingRequirementKind]json.RawMessage{orders.RequirementGTD: w.GTD, orders.RequirementRNPT: w.RNPT, orders.RequirementMandatoryMark: w.Mark, orders.RequirementJewelryUIN: w.Jewelry, orders.RequirementWeight: w.Weight} {
		f := newOrderFields([]byte(`{}`))
		if len(raw) > 0 {
			f.raw["value"] = raw
		}
		p.Required[kind] = f.boolean("value")
		if *f.err != nil {
			return p, errOrderPayload
		}
	}
	for _, unit := range w.Exemplars {
		u, _, e := decodeExemplarUnit(unit)
		if e != nil {
			return p, e
		}
		p.Exemplars = append(p.Exemplars, u)
	}
	return p, nil
}

func decodeExemplarUnit(w exemplarWire) (orders.ExemplarInput, orders.ExemplarCheck, error) {
	id, e := orderID(w.ID)
	if e != nil {
		return orders.ExemplarInput{}, orders.ExemplarCheck{}, e
	}
	f := newOrderFields([]byte(`{}`))
	for k, v := range map[string]json.RawMessage{"gtd": w.GTD, "ga": w.GTDAbsent, "rnpt": w.RNPT, "ra": w.RNPTAbsent} {
		if len(v) > 0 {
			f.raw[k] = v
		}
	}
	u := orders.ExemplarInput{ID: id, GTD: f.text("gtd"), GTDAbsent: f.boolean("ga"), RNPT: f.text("rnpt"), RNPTAbsent: f.boolean("ra")}
	check := orders.ExemplarCheck{ID: id, GTD: w.GTDCheck, RNPT: w.RNPTCheck}
	for _, code := range append(w.GTDErrors, w.RNPTErrors...) {
		check.Errors = append(check.Errors, labelErrorCode(code))
	}
	if err := decodeExemplarMarks(w.Marks, &u, &check); err != nil {
		return u, check, err
	}

	if *f.err != nil {
		return u, check, errOrderPayload
	}
	for _, state := range append([]string{check.GTD, check.RNPT}, check.Marks...) {
		if state != "" && !slices.Contains([]string{"processing", "failed", "passed"}, state) {
			return u, check, orders.ErrSchemaDrift
		}
	}
	return u, check, nil
}

func (c *OrdersClient) ReadOrderExemplarStatus(ctx context.Context, scope orders.Scope, posting string) (orders.ExemplarStatus, error) {
	if scope != c.scope || posting == "" || len(posting) > 256 {
		return orders.ExemplarStatus{}, orders.ErrInvalid
	}
	data, err := c.orderPost(ctx, exemplarStatusPath, map[string]string{"posting_number": posting})
	if err != nil {
		return orders.ExemplarStatus{}, err
	}
	var w exemplarResponseWire
	if decodeLabelJSON(data, &w) != nil || w.Posting != posting || !slices.Contains([]string{"ship_available", "ship_not_available", "validation_in_process", "update_available", "update_not_available"}, w.Status) {
		return orders.ExemplarStatus{}, orders.ErrSchemaDrift
	}
	result := orders.ExemplarStatus{PostingNumber: posting, Status: w.Status, Products: make([]orders.ExemplarStatusProduct, 0, len(w.Products))}
	for _, row := range w.Products {
		sku, e := orderID(row.ID)
		if e != nil {
			return result, e
		}
		p := orders.ExemplarStatusProduct{SKU: sku}
		for _, value := range row.Exemplars {
			u, check, e := decodeExemplarUnit(value)
			if e != nil {
				return result, e
			}
			p.Exemplars = append(p.Exemplars, u)
			p.Checks = append(p.Checks, check)
		}
		result.Products = append(result.Products, p)
	}
	return result, nil
}

func decodeExemplarMarks(raw json.RawMessage, u *orders.ExemplarInput, check *orders.ExemplarCheck) error {
	if len(raw) > 0 && string(raw) != "null" {
		var marks []exemplarMarkWire
		if decodeLabelJSON(raw, &marks) != nil {
			return orders.ErrSchemaDrift
		}
		values := make([]orders.ExemplarMark, 0, len(marks))
		for _, m := range marks {
			if !slices.Contains([]string{"mandatory_mark", "jw_uin", "imei"}, m.Type) {
				return orders.ErrSchemaDrift
			}
			values = append(values, orders.ExemplarMark{Type: m.Type, Value: m.Value})
			check.Marks = append(check.Marks, m.Status)
			for _, code := range m.Errors {
				check.Errors = append(check.Errors, labelErrorCode(code))
			}
		}
		u.Marks = orders.Present(values)
	}
	return nil
}
