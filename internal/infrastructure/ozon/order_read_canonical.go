package ozon

import (
	"slices"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (c *OrdersClient) decodeOrderPosting(f orderFields, observed time.Time) (orders.Posting, error) {
	posting := orders.Posting{
		Scope: c.scope, CommercialOrderID: orderKnown(f.id("order_id")), PostingNumber: orderKnown(f.text("posting_number")),
		OrderNumber: f.text("order_number"), ParentPostingNumber: f.text("parent_posting_number"), ChildPostingNumbers: f.strings("child_posting_numbers"),
		Status: f.text("status"), Substatus: f.text("substatus"), AvailableActions: f.strings("available_actions"),
		Evidence: orders.EvidenceRevision{Revision: "pending", Origin: orders.OriginOzon, ObservedAt: observed, CheckedAt: observed,
			ProviderUpdatedAt: f.timestamp("updated_at"), ProviderVersion: f.text("version"), CollectionKind: orders.CollectionSnapshot},
	}
	posting.Scheme = orderScheme(f, &posting)
	posting.Times = orderTimes(f)
	posting.Cancellation = orderCancellation(f)
	posting.Lines = orderLines(f, &posting)
	decodeOrderDelivery(f, &posting)
	decodeOrderFinancial(f, &posting)
	decodeOrderAnalytics(f, &posting)
	c.decodeOrderCustomer(f, &posting)
	addOrderUnsupportedGroups(f, &posting)
	posting.Drift = append(posting.Drift, *f.drift...)
	if *f.err != nil || posting.CommercialOrderID == "" || posting.PostingNumber == "" {
		return orders.Posting{}, errOrderPayload
	}
	return posting, nil
}

func orderTimes(f orderFields) orders.PostingTimes {
	return orders.PostingTimes{
		CreatedAt: f.timestamp("created_at"), InProcessAt: f.timestamp("in_process_at"), ShipmentAt: f.timestamp("shipment_date"),
		DeliveringAt: f.timestamp("delivering_date"), DeliveredAt: f.timestamp("fact_delivery_date"), ShipmentWithoutDelayAt: f.timestamp("shipment_date_without_delay"),
	}
}

func orderScheme(f orderFields, posting *orders.Posting) orders.Field[string] {
	scheme := f.text("scheme")
	if scheme.IsMissing() {
		scheme = f.text("fulfillment_scheme")
	}
	isFBO := f.boolean("is_fbo")
	if isFBO.IsKnown() && *isFBO.Value {
		scheme = orders.Present("FBO")
	}
	if !scheme.IsKnown() {
		posting.Drift = append(posting.Drift, orders.Drift{Path: "$.result.scheme", Code: "scheme_unverified"})
		return scheme
	}
	if *scheme.Value == "FBO" {
		posting.Drift = append(posting.Drift, orders.Drift{Path: "$.result.scheme", Code: "fbo_out_of_scope"})
	}
	if !slices.Contains([]string{"FBS", "rFBS", "FBO"}, *scheme.Value) {
		posting.Drift = append(posting.Drift, orders.Drift{Path: "$.result.scheme", Code: "unknown_enum"})
	}
	return scheme
}

func orderCancellation(f orderFields) orders.Field[orders.Cancellation] {
	data, present := f.raw["cancellation"]
	if !present {
		return orders.Field[orders.Cancellation]{Presence: orders.PresenceMissing}
	}
	if string(data) == "null" {
		return orders.Field[orders.Cancellation]{Presence: orders.PresenceNull}
	}
	child := f.object("cancellation")
	if len(child.raw) == 0 {
		return orders.Field[orders.Cancellation]{Presence: orders.PresenceEmpty}
	}
	reason := child.text("cancel_reason")
	return orders.Present(orders.Cancellation{ReasonID: child.id("cancel_reason_id"), ReasonPresence: reason.Presence,
		Type: child.text("cancellation_type"), Initiator: child.text("cancellation_initiator"), AfterShipment: child.boolean("cancelled_after_ship")})
}

func orderLines(f orderFields, posting *orders.Posting) []orders.PostingLine {
	rows := f.array("products")
	if rows == nil {
		f.fail()
		return nil
	}
	lines := make([]orders.PostingLine, 0, len(rows))
	for _, row := range rows {
		quantity := row.decimal("quantity")
		if !quantity.IsKnown() {
			f.fail()
			return nil
		}
		line := orders.PostingLine{Identity: orders.LineIdentity{ProviderLineID: orderKnown(row.text("line_id")), SKU: orderKnown(row.id("sku")), OfferID: orderKnown(row.text("offer_id"))},
			ProductID: row.id("product_id"), Quantity: *quantity.Value, CancelledQuantity: row.decimal("cancelled_quantity"), Name: row.text("name"), VAT: row.decimal("vat")}
		line.Price = orderMoney(row, "price", row.text("currency_code"), posting, "$.result.products[].price")
		lines = append(lines, line)
	}
	return lines
}

func decodeOrderDelivery(f orderFields, posting *orders.Posting) {
	method := f.object("delivery_method")
	posting.WarehouseID, posting.DeliveryMethodID = method.id("warehouse_id"), method.id("id")
	posting.Delivery = orders.DeliveryFacts{Presence: f.objectPresence("delivery_method"), ProviderID: method.id("provider_id"), TPLProviderID: method.id("tpl_provider_id"), IntegrationType: f.text("tpl_integration_type"), Express: f.boolean("is_express"), ProviderName: method.text("tpl_provider"), TrackingNumber: f.text("tracking_number")}
}

func decodeOrderAnalytics(f orderFields, posting *orders.Posting) {
	analytics := f.object("analytics_data")
	posting.Analytics = orders.AnalyticsFacts{Presence: f.objectPresence("analytics_data"), Region: analytics.text("region"), City: analytics.text("city"), DeliveryType: analytics.text("delivery_type"), Premium: analytics.boolean("is_premium")}
	posting.Delivery.WindowFrom = analytics.timestamp("delivery_date_begin")
	posting.Delivery.WindowTo = analytics.timestamp("delivery_date_end")
}

func decodeOrderFinancial(f orderFields, posting *orders.Posting) {
	financial := f.object("financial_data")
	posting.Financial.Presence = f.objectPresence("financial_data")
	posting.Financial.Currency = financial.text("currency_code")
	rows := financial.array("products")
	posting.Financial.Lines = make([]orders.FinancialLine, 0, len(rows))
	for _, row := range rows {
		currency := row.text("currency_code")
		commissionCurrency := row.text("commissions_currency_code")
		line := orders.FinancialLine{ProviderProductID: row.id("product_id"),
			Identity: orders.LineIdentity{ProviderLineID: orderKnown(row.text("line_id")), SKU: orderKnown(row.id("sku")), OfferID: orderKnown(row.text("offer_id"))}, VAT: row.decimal("vat")}
		line.PriceAmount = row.decimal("price")
		line.Price = orderMoney(row, "price", currency, posting, "$.result.financial_data.products[].price")
		line.OldPrice = orderMoney(row, "old_price", currency, posting, "$.result.financial_data.products[].old_price")
		line.Commission = orderMoney(row, "commission_amount", commissionCurrency, posting, "$.result.financial_data.products[].commission_amount")
		line.Payout = orderMoney(row, "payout", currency, posting, "$.result.financial_data.products[].payout")
		posting.Financial.Lines = append(posting.Financial.Lines, line)
	}
}

func orderMoney(f orderFields, key string, currency orders.Field[string], posting *orders.Posting, path string) orders.Field[orders.Money] {
	amount := f.decimal(key)
	if !amount.IsKnown() {
		return orders.Field[orders.Money]{Presence: amount.Presence}
	}
	if !currency.IsKnown() {
		posting.Drift = append(posting.Drift, orders.Drift{Path: path, Code: "missing_currency"})
		return orders.Field[orders.Money]{Presence: orders.PresenceMissing}
	}
	money := orders.Money{Amount: *amount.Value, Currency: *currency.Value}
	if money.Validate() != nil {
		f.fail()
	}
	return orders.Present(money)
}

func (c *OrdersClient) decodeOrderCustomer(f orderFields, posting *orders.Posting) {
	data, present := f.raw["customer"]
	posting.Customer.Presence = orders.PresenceMissing
	if !present {
		return
	}
	posting.Customer.Presence = orders.PresenceNull
	if string(data) == "null" {
		return
	}
	customer := f.object("customer")
	posting.Customer.Presence = orders.PresenceEmpty
	if len(customer.raw) == 0 {
		return
	}
	posting.Customer.Presence = orders.PresenceValue
	if !c.options.CustomerPII {
		return
	}
	posting.PII = &orders.Customer{Name: customer.text("name"), Phone: customer.text("phone"), Email: customer.text("email")}
	posting.PII.Address = orderCustomerAddress(customer)
}

func orderCustomerAddress(customer orderFields) orders.Field[orders.Address] {
	data, present := customer.raw["address"]
	if !present {
		return orders.Field[orders.Address]{Presence: orders.PresenceMissing}
	}
	if string(data) == "null" {
		return orders.Field[orders.Address]{Presence: orders.PresenceNull}
	}
	address := customer.object("address")
	if len(address.raw) == 0 {
		return orders.Field[orders.Address]{Presence: orders.PresenceEmpty}
	}
	return orders.Present(orders.Address{Country: address.text("country"), Region: address.text("region"), City: address.text("city"),
		PostalCode: address.text("zip_code"), Street: address.text("address_tail"), House: address.text("house"), Apartment: address.text("apartment")})
}

func addOrderUnsupportedGroups(f orderFields, posting *orders.Posting) {
	for _, key := range []string{"barcodes", "legal_info", "requirements", "tariffication", "optional", "addressee", "quantum_id", "scanit", "pickup_code_verified_at"} {
		if _, present := f.raw[key]; present {
			posting.Drift = append(posting.Drift, orders.Drift{Path: "$.result." + key, Code: "unsupported_typed_field"})
		}
	}
}

func (c *OrdersClient) orderDrift(path string, data []byte, drift []orders.Drift) []orders.Drift {
	report := c.manifest.Validate(path, "response", data)
	if !report.SchemaVerified {
		drift = append(drift, orders.Drift{Path: "$", Code: "schema_unverified"})
	}
	for _, issue := range report.Issues {
		safe := strings.ReplaceAll(strings.ReplaceAll(issue.Path, "<unknown:", "unknown:"), ">", "")
		drift = append(drift, orders.Drift{Path: safe, Code: issue.Code})
	}
	return drift
}

func finishOrderPosting(posting orders.Posting) (orders.Posting, error) {
	if posting.Status.IsKnown() && !knownOrderStatus(*posting.Status.Value) {
		posting.Drift = append(posting.Drift, orders.Drift{Path: "$.result.status", Code: "unknown_enum"})
	}
	slices.SortFunc(posting.Drift, func(a, b orders.Drift) int { return strings.Compare(a.Path+":"+a.Code, b.Path+":"+b.Code) })
	posting.Drift = slices.Compact(posting.Drift)
	revision, err := orders.PostingFingerprint(posting)
	if err != nil {
		return orders.Posting{}, errOrderPayload
	}
	posting.Evidence.Revision = revision
	return posting, nil
}

func knownOrderStatus(status string) bool {
	return slices.Contains([]string{"awaiting_registration", "acceptance_in_progress", "awaiting_approve", "awaiting_packaging", "awaiting_deliver", "delivering", "driver_pickup", "delivered", "cancelled", "not_accepted", "arbitration", "client_arbitration"}, status)
}
