package ozon

import "github.com/lilter96/styk/internal/orders"

type accrualNode struct {
	kind   string
	bits   int
	fields map[string]accrualNode
	item   *accrualNode
}

func accrualText() accrualNode        { return accrualNode{kind: "STRING"} }
func accrualInt(bits int) accrualNode { return accrualNode{kind: "INTEGER", bits: bits} }
func accrualDecimal() accrualNode     { return accrualNode{kind: "DECIMAL"} }
func accrualObject(fields map[string]accrualNode) accrualNode {
	return accrualNode{kind: "OBJECT", fields: fields}
}
func accrualArray(item accrualNode) accrualNode { return accrualNode{kind: "ARRAY", item: &item} }
func accrualMoney() accrualNode {
	return accrualObject(map[string]accrualNode{"amount": accrualDecimal(), "currency": accrualText()})
}
func accrualFee() accrualNode {
	return accrualObject(map[string]accrualNode{"accrued": accrualMoney(), "type_id": accrualInt(32)})
}

func accrualSchema(kind orders.AccrualKind) accrualNode {
	switch kind {
	case orders.AccrualTypes:
		row := accrualObject(map[string]accrualNode{"id": accrualInt(32), "name": accrualText(), "description": accrualText()})
		return accrualObject(map[string]accrualNode{"accrual_types": accrualArray(row)})
	case orders.AccrualPostings:
		row := accrualObject(map[string]accrualNode{"accrual_date": accrualText(), "accrued": accrualMoney(), "quantity": accrualInt(32), "seller_price": accrualMoney(), "sku": accrualInt(64), "type_id": accrualInt(32)})
		posting := accrualObject(map[string]accrualNode{"posting_number": accrualText(), "accruals": accrualArray(row)})
		return accrualObject(map[string]accrualNode{"posting_accruals": accrualArray(posting)})
	default:
		return accrualObject(map[string]accrualNode{"accruals": accrualArray(accrualDayRow()), "last_id": accrualText()})
	}
}

func accrualDayRow() accrualNode {
	commission := accrualObject(map[string]accrualNode{"bonus": accrualMoney(), "coinvestment": accrualMoney(), "commission": accrualMoney(), "commission_ratio": accrualText(), "sale_amount": accrualMoney(), "sale_commission": accrualMoney(), "sale_price": accrualMoney(), "seller_price": accrualMoney()})
	delivery := accrualObject(map[string]accrualNode{"services": accrualArray(accrualFee()), "total_accrued": accrualMoney()})
	product := accrualObject(map[string]accrualNode{"sku": accrualInt(64), "commission": commission, "delivery": delivery})
	posting := accrualObject(map[string]accrualNode{"delivery_schema": accrualText(), "delivery_speed": accrualInt(32), "products": accrualArray(product)})
	item := accrualObject(map[string]accrualNode{"sku": accrualInt(64), "fees": accrualArray(accrualFee())})
	return accrualObject(map[string]accrualNode{"accrued_category": accrualText(), "accrual_id": accrualInt(64), "date": accrualText(), "unit_number": accrualText(), "total_amount": accrualMoney(), "posting": posting, "item_fees": accrualObject(map[string]accrualNode{"fees": accrualArray(item)}), "non_item_fee": accrualFee(), "container_fees": accrualObject(map[string]accrualNode{"fees": accrualArray(accrualFee())})})
}
