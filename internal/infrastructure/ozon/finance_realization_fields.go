package ozon

import (
	"strconv"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func realizationAmounts(parent orderFields, key string) orders.RealizationAmounts {
	field := parent.object(key)
	var pickUpPoint orders.Field[orders.Decimal]
	if _, present := field.raw["pick_up_point_coinvestment"]; present {
		pickUpPoint = realizationDecimal(field, "pick_up_point_coinvestment")
	}
	quantity := realizationDecimal(field, "quantity")
	if quantity.IsKnown() {
		if _, err := strconv.ParseInt(string(*quantity.Value), 10, 32); err != nil {
			field.fail()
		}
	}
	return orders.RealizationAmounts{Presence: parent.objectPresence(key), Quantity: quantity,
		Amount: realizationDecimal(field, "amount"), Bonus: realizationDecimal(field, "bonus"),
		LegacyCommission: realizationDecimal(field, "commission"), Compensation: realizationDecimal(field, "compensation"),
		PricePerInstance: realizationDecimal(field, "price_per_instance"), StandardFee: realizationDecimal(field, "standard_fee"),
		BankCoinvestment: realizationDecimal(field, "bank_coinvestment"), PickUpPointCoinvestment: pickUpPoint, Stars: realizationDecimal(field, "stars"), Total: realizationDecimal(field, "total")}
}

func realizationDecimal(field orderFields, key string) orders.Field[orders.Decimal] {
	raw := field.raw[key]
	if len(raw) > 0 && raw[0] == '"' {
		field.fail()
	}
	return field.decimal(key)
}

func realizationText(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

func realizationPeriodConfirmed(report orders.RealizationReport) bool {
	start, stop := report.StartDate, report.StopDate
	if !start.IsKnown() || !stop.IsKnown() {
		return false
	}
	from, err := time.Parse("2006-01-02", *start.Value)
	if err != nil {
		return false
	}
	to, err := time.Parse("2006-01-02", *stop.Value)
	if err != nil {
		return false
	}
	want := time.Date(report.Period.Year, time.Month(report.Period.Month), 1, 0, 0, 0, 0, time.UTC)
	return from.Equal(want) && to.Equal(want.AddDate(0, 1, -1))
}

func realizationDate(field orderFields, key string) orders.Field[string] {
	value := field.text(key)
	if value.IsKnown() && (len(*value.Value) > 40 || strings.Trim(*value.Value, "0123456789T:Z.+- /") != "") {
		field.fail()
	}
	return value
}
