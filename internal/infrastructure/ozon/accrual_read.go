package ozon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

const accrualTypesPath = "/v1/finance/accrual/types"
const accrualDayPath = "/v1/finance/accrual/by-day"
const accrualPostingsPath = "/v1/finance/accrual/postings"

func accrualReadPath(path string) bool {
	return path == accrualTypesPath || path == accrualDayPath || path == accrualPostingsPath
}

func (c *OrdersClient) ReadAccrualReport(ctx context.Context, scope orders.Scope, request orders.AccrualRequest) (orders.AccrualReport, error) {
	if scope != c.scope || request.Validate(c.now()) != nil {
		return orders.AccrualReport{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	report := orders.AccrualReport{Scope: scope, SourceFamily: "ACCRUAL", Kind: request.Kind, Request: request, Facts: []orders.AccrualFact{}, UnknownFields: []orders.AccrualUnknownField{}, ContractVersion: "ozon-accrual-v1-2026-11-07", Blockers: []orders.Validation{{Code: "ACCRUAL_DIAGNOSTIC_NOT_MONTHLY_REALIZATION"}, {Code: "ACCRUAL_NOT_NATIVE_ACCOUNTING_OR_PAYOUT"}}}
	var err error
	if request.Kind == orders.AccrualByDay {
		err = c.readAccrualDay(ctx, &report)
	} else {
		err = c.readAccrualSingle(ctx, &report)
	}
	if err != nil {
		return orders.AccrualReport{}, accrualReadError(err)
	}
	return c.finishAccrualReport(report)
}

func accrualReadError(err error) error {
	var api *APIError
	if !errors.As(err, &api) {
		return err
	}
	code := "ACCRUAL_PROVIDER_UNAVAILABLE"
	switch api.Status {
	case 401, 403:
		code = "ACCRUAL_PERMISSION_REQUIRED"
	case 404:
		code = "ACCRUAL_SOURCE_NOT_FOUND"
	case 429:
		code = "ACCRUAL_RATE_LIMITED"
	}
	return &orders.AccrualReadError{Code: code, Cause: err}
}

func (c *OrdersClient) readAccrualSingle(ctx context.Context, report *orders.AccrualReport) error {
	path, body := accrualTypesPath, map[string]any{}
	if report.Kind == orders.AccrualPostings {
		path, body = accrualPostingsPath, map[string]any{"posting_numbers": report.Request.PostingNumbers}
	}
	data, err := c.orderPost(ctx, path, body)
	if err != nil {
		return err
	}
	if err = appendAccrualPage(report, data); err != nil {
		return err
	}
	if report.Kind == orders.AccrualTypes {
		report.Types, err = decodeAccrualTypes(data)
	} else {
		err = validateAccrualPostings(data, report.Request.PostingNumbers)
	}
	if err != nil {
		return err
	}
	report.Complete = true
	return nil
}

func (c *OrdersClient) readAccrualDay(ctx context.Context, report *orders.AccrualReport) error {
	last := ""
	seenCursors := make(map[string]bool, 20)
	seenIDs := make(map[string]bool)
	for page := 0; page < 20; page++ {
		data, err := c.orderPost(ctx, accrualDayPath, map[string]any{"date": report.Request.Date, "last_id": last})
		if err != nil {
			return err
		}
		if err = validateAccrualDay(data, report.Request.Date, seenIDs); err != nil {
			return err
		}
		if err = appendAccrualPage(report, data); err != nil {
			return err
		}
		f := newOrderFields(data)
		last, err = orderString(f.raw["last_id"])
		if err != nil || f.raw["last_id"] == nil || string(f.raw["last_id"]) == "null" || len(last) > 4096 {
			return orders.ErrSchemaDrift
		}
		if last == "" {
			report.Complete = true
			return nil
		}
		if seenCursors[last] {
			return orders.ErrSchemaDrift
		}
		seenCursors[last] = true
	}
	return &LimitError{Resource: "accrual_pages"}
}

func appendAccrualPage(report *orders.AccrualReport, data []byte) error {
	if ordercontract.CheckJSON(data) != nil {
		return orders.ErrSchemaDrift
	}
	root := newOrderFields(data)
	arrayKey := "accruals"
	if report.Kind == orders.AccrualTypes {
		arrayKey = "accrual_types"
	}
	if report.Kind == orders.AccrualPostings {
		arrayKey = "posting_accruals"
	}
	if root.raw[arrayKey] == nil || string(root.raw[arrayKey]) == "null" {
		return orders.ErrSchemaDrift
	}
	rows := root.array(arrayKey)
	if *root.err != nil || report.RecordCount+len(rows) > 10000 {
		return orders.ErrSchemaDrift
	}
	decoder := accrualDecoder{offset: report.RecordCount, facts: make([]orders.AccrualFact, 0), unknown: make([]orders.AccrualUnknownField, 0)}
	if err := decoder.node(data, "$", accrualSchema(report.Kind)); err != nil {
		return err
	}
	if len(report.Facts)+len(decoder.facts) > 100000 || len(report.UnknownFields)+len(decoder.unknown) > 10000 {
		return &LimitError{Resource: "accrual_facts"}
	}
	for _, fact := range decoder.facts {
		if fact.Path != "last_id" {
			report.Facts = append(report.Facts, fact)
		}
	}
	report.UnknownFields = append(report.UnknownFields, decoder.unknown...)
	report.RecordCount += len(rows)
	report.Pages++
	return nil
}

func validateAccrualDay(data []byte, date string, seen map[string]bool) error {
	if ordercontract.CheckJSON(data) != nil {
		return orders.ErrSchemaDrift
	}
	root := newOrderFields(data)
	for _, row := range root.array("accruals") {
		numeric, err := strconv.ParseInt(string(row.raw["accrual_id"]), 10, 64)
		id := strconv.FormatInt(numeric, 10)
		if err != nil || id != string(row.raw["accrual_id"]) || seen[id] || orderKnown(row.text("date")) != date {
			return orders.ErrSchemaDrift
		}
		seen[id] = true
	}
	if *root.err != nil {
		return orders.ErrSchemaDrift
	}
	return nil
}

func validateAccrualPostings(data []byte, requested []string) error {
	root := newOrderFields(data)
	seen := make(map[string]bool, len(requested))
	for _, row := range root.array("posting_accruals") {
		number := orderKnown(row.text("posting_number"))
		if !slices.Contains(requested, number) || seen[number] || row.raw["accruals"] == nil || string(row.raw["accruals"]) == "null" {
			return orders.ErrSchemaDrift
		}
		seen[number] = true
	}
	if *root.err != nil || len(seen) != len(requested) {
		return orders.ErrSchemaDrift
	}
	return nil
}

func decodeAccrualTypes(data []byte) ([]orders.AccrualType, error) {
	root := newOrderFields(data)
	rows := root.array("accrual_types")
	result := make([]orders.AccrualType, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		id, err := strconv.ParseInt(string(row.raw["id"]), 10, 32)
		name, description := orderKnown(row.text("name")), orderKnown(row.text("description"))
		if err != nil || strconv.FormatInt(id, 10) != string(row.raw["id"]) || name == "" || seen[strconv.FormatInt(id, 10)] {
			return nil, orders.ErrSchemaDrift
		}
		value := strconv.FormatInt(id, 10)
		seen[value] = true
		result = append(result, orders.AccrualType{ID: value, Name: name, Description: description})
	}
	if *root.err != nil {
		return nil, orders.ErrSchemaDrift
	}
	return result, nil
}

func (c *OrdersClient) finishAccrualReport(report orders.AccrualReport) (orders.AccrualReport, error) {
	slices.SortFunc(report.UnknownFields, func(a, b orders.AccrualUnknownField) int { return strings.Compare(a.Path, b.Path) })
	if len(report.UnknownFields) > 0 {
		report.Complete = false
		report.Blockers = append(report.Blockers, orders.Validation{Code: "ACCRUAL_UNKNOWN_FIELDS_REQUIRE_REVIEW"})
	}
	report.ObservedAt = time.Time{}
	data, err := json.Marshal(report)
	if err != nil {
		return report, err
	}
	hash := sha256.Sum256(data)
	report.Fingerprint = hex.EncodeToString(hash[:])
	report.ObservedAt = c.now()
	return report, nil
}
