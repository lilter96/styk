// Package doctor describes diagnostic evidence without making network requests.
package doctor

import (
	"sort"
	"time"
)

type Status string

const (
	Pass       Status = "PASS"
	Warn       Status = "WARN"
	Fail       Status = "FAIL"
	NotChecked Status = "NOT_CHECKED"
)

// Batch contains only aggregate data; it intentionally has no customer data.
type Batch struct {
	Age              string           `json:"age"`
	Exhaustive       bool             `json:"exhaustive"`
	PointInTime      bool             `json:"point_in_time"`
	ID               int64            `json:"id"`
	AvailableBatches int64            `json:"available_batches"`
	Scope            string           `json:"-"`
	Present          bool             `json:"present"`
	Completed        bool             `json:"completed"`
	ObservedAt       time.Time        `json:"observed_at,omitempty"`
	WindowFrom       *time.Time       `json:"window_from,omitempty"`
	WindowTo         *time.Time       `json:"window_to,omitempty"`
	Counts           map[string]int64 `json:"counts,omitempty"`
}

type Evidence struct {
	CalculationReferences                      map[string]int64
	MoyskladRegistered, OzonRegistered         bool
	Moysklad, Ozon, Postings, Orders           Batch
	ProductMappings, WarehouseMappings         int64
	UnconfirmedProducts, AmbiguousCorrelations int64
	CorrelationsChecked, CorrelationsTruncated bool
	CorrelationStatuses                        map[string]int64
	DecisionStatus                             string
	DecisionReasons                            []string
}

type Check struct {
	Code   string `json:"code"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
}

type Report struct {
	EvaluatedAt           time.Time        `json:"evaluated_at"`
	CalculationReferences map[string]int64 `json:"calculation_references"`
	Checks                []Check          `json:"checks"`
	Batches               map[string]Batch `json:"batches"`
	DecisionStatus        string           `json:"decision_status"`
	DecisionReasons       []string         `json:"decision_reasons"`
	MappingCounts         map[string]int64 `json:"mapping_counts"`
	CorrelationStatuses   map[string]int64 `json:"correlation_statuses"`
	CorrelationsTruncated bool             `json:"correlations_truncated"`
	Publication           string           `json:"publication"`
}

// Evaluate reports only the facts supplied by the scoped persistence layer.
// A finished traversal never establishes exhaustive or point-in-time coverage.
func Evaluate(e Evidence, now time.Time, maxAge time.Duration) Report {
	r := Report{Batches: map[string]Batch{"moysklad": e.Moysklad, "ozon": e.Ozon, "postings": e.Postings, "customer_orders": e.Orders}, DecisionStatus: e.DecisionStatus, DecisionReasons: append([]string{}, e.DecisionReasons...), Publication: "DISABLED"}
	r.MappingCounts = map[string]int64{"confirmed_products": e.ProductMappings, "unconfirmed_products": e.UnconfirmedProducts, "confirmed_warehouses": e.WarehouseMappings}
	r.EvaluatedAt = now.UTC()
	r.CalculationReferences = e.CalculationReferences
	r.CorrelationStatuses = e.CorrelationStatuses
	r.CorrelationsTruncated = e.CorrelationsTruncated
	if r.DecisionStatus != "UNKNOWN" && r.DecisionStatus != "BLOCKED" {
		r.DecisionStatus = "NOT_CHECKED"
	}
	for k, b := range r.Batches {
		b.Exhaustive = false
		b.PointInTime = false
		if !b.ObservedAt.IsZero() {
			b.Age = now.Sub(b.ObservedAt).String()
		}
		r.Batches[k] = b
	}
	sort.Strings(r.DecisionReasons)
	add := func(code string, status Status, detail string) {
		r.Checks = append(r.Checks, Check{code, status, detail})
	}
	if e.MoyskladRegistered && e.OzonRegistered {
		add("ACCOUNT_REGISTRATION", Pass, "Both accounts are registered to the selected tenant; registration is an administrative attestation.")
	} else {
		add("ACCOUNT_REGISTRATION", Fail, "Both accounts must be registered to the selected tenant.")
	}
	for _, c := range []struct {
		code       string
		registered bool
	}{{"MOYSKLAD_REGISTRATION", e.MoyskladRegistered}, {"OZON_REGISTRATION", e.OzonRegistered}} {
		if c.registered {
			add(c.code, Pass, "Account is registered to the selected tenant; registration is an administrative attestation.")
		} else {
			add(c.code, NotChecked, "Account is not registered to the selected tenant; its observations were not accessed.")
		}
	}
	add("MOYSKLAD_CONNECTION", NotChecked, "No live connection check was performed.")
	add("OZON_CONNECTION", NotChecked, "No live connection check was performed.")
	for _, entry := range []struct {
		code       string
		batch      Batch
		registered bool
	}{{"MOYSKLAD_IMPORT", e.Moysklad, e.MoyskladRegistered}, {"OZON_IMPORT", e.Ozon, e.OzonRegistered}, {"POSTING_IMPORT", e.Postings, e.OzonRegistered}, {"CUSTOMER_ORDER_IMPORT", e.Orders, e.MoyskladRegistered}} {
		switch {
		case !entry.registered:
			add(entry.code, NotChecked, "Source account is not registered to this tenant.")
		case !entry.batch.Present:
			add(entry.code, Warn, "No eligible import is available.")
		case !entry.batch.Completed:
			add(entry.code, Fail, "The selected traversal is incomplete.")
		case entry.batch.ObservedAt.IsZero() || now.Before(entry.batch.ObservedAt) || maxAge <= 0 || now.Sub(entry.batch.ObservedAt) > maxAge:
			add(entry.code, Warn, "The local observation time is missing, invalid or stale.")
		default:
			add(entry.code, Pass, "A completed traversal was recorded within the configured local age limit.")
		}
	}
	for _, c := range []struct {
		code       string
		registered bool
		batch      Batch
	}{
		{"MOYSKLAD_STOCK_COVERAGE", e.MoyskladRegistered, e.Moysklad},
		{"OZON_STOCK_COVERAGE", e.OzonRegistered, e.Ozon},
	} {
		if !c.registered {
			add(c.code, NotChecked, "Source account is not registered to this tenant.")
		} else if !c.batch.Present {
			add(c.code, NotChecked, "No eligible source stock import was found.")
		} else {
			add(c.code, Warn, "Reported stock rows do not establish exhaustive or point-in-time coverage; missing rows are unknown.")
		}
	}
	if !e.MoyskladRegistered || !e.OzonRegistered {
		add("PRODUCT_MAPPINGS", NotChecked, "Both source accounts must be registered before cross-system checks.")
	} else if e.ProductMappings == 0 || e.UnconfirmedProducts > 0 {
		add("PRODUCT_MAPPINGS", Warn, "Confirmed mappings are missing or products remain unconfirmed.")
	} else {
		add("PRODUCT_MAPPINGS", Pass, "Confirmed mappings exist for the selected scope.")
	}
	if !e.MoyskladRegistered || !e.OzonRegistered {
		add("WAREHOUSE_MAPPINGS", NotChecked, "Both source accounts must be registered before cross-system checks.")
	} else if e.WarehouseMappings == 0 {
		add("WAREHOUSE_MAPPINGS", Warn, "No confirmed warehouse mapping exists for the selected scope.")
	} else {
		add("WAREHOUSE_MAPPINGS", Pass, "A confirmed warehouse mapping exists for the selected scope.")
	}
	switch {
	case !e.CorrelationsChecked:
		add("ORDER_CORRELATIONS", NotChecked, "Correlation was not evaluated.")
	case e.CorrelationsTruncated:
		add("ORDER_CORRELATIONS", Warn, "Correlation results were truncated; coverage is incomplete.")
	case e.AmbiguousCorrelations > 0:
		add("ORDER_CORRELATIONS", Warn, "Ambiguous correlations require review.")
	default:
		add("ORDER_CORRELATIONS", Warn, "Matching candidates do not prove cross-system order identity.")
	}
	add("MATCHING_SUGGESTIONS", NotChecked, "Suggestions are generated on demand by the matching CLI; unconfirmed product counts are not candidate counts.")
	if r.DecisionStatus == "NOT_CHECKED" {
		add("CALCULATION_SCOPE", NotChecked, "No confirmed product and warehouse mapping pair was evaluated; no decision or command was persisted.")
	} else {
		add("CALCULATION_SCOPE", Warn, "Only the first deterministic mapped item/warehouse pair is evaluated with diagnostic policy: piece, safety 0; other pools are not checked. No decision or command is persisted.")
	}
	add("EXISTING_EXPOSURE", Warn, "Existing marketplace listings may remain exposed; this report cannot withdraw stock or establish safe capacity.")
	add("COVERAGE", Warn, "Completed traversal is not exhaustive or a point-in-time snapshot; older active obligations may be outside the selected windows.")
	add("SOURCE_ORDERING", Warn, "Source event ordering is not established.")
	add("SOURCE_PERMISSIONS", NotChecked, "Source account visibility and permissions were not verified.")
	add("RESERVATION_ATTRIBUTION", Warn, "Posting-to-reserve attribution and stock-baseline inclusion are not established.")
	sort.Slice(r.Checks, func(i, j int) bool { return r.Checks[i].Code < r.Checks[j].Code })
	return r
}
