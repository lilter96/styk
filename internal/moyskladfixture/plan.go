// Package moyskladfixture contains isolated test-account fixture tooling.
// It is not used by the API, worker or integration clients.
package moyskladfixture

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"
)

type State string

const (
	Planned          State = "PLANNED"
	Created          State = "CREATED_AND_VERIFIED"
	FailedBeforeSend State = "FAILED_BEFORE_SEND"
	Unknown          State = "UNKNOWN_OUTCOME"
	Skipped          State = "SKIPPED"
)

var namespacePattern = regexp.MustCompile(`^STYK-IT-[0-9]{8}-[a-f0-9]{12}$`)

// Operation contains only synthetic fixture information and remote IDs, never credentials.
type Operation struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
	State    State  `json:"state"`
	RemoteID string `json:"remote_id,omitempty"`
	Note     string `json:"note,omitempty"`
}

type Manifest struct {
	Version          int                    `json:"version"`
	Namespace        string                 `json:"namespace"`
	AccountHash      string                 `json:"account_hash"`
	InitialCounts    map[string]int         `json:"initial_counts,omitempty"`
	NamespaceChecked bool                   `json:"namespace_checked"`
	StockCheckedAt   *time.Time             `json:"stock_checked_at,omitempty"`
	StockChecks      map[string]StockResult `json:"stock_checks,omitempty"`
	StockMayBeStale  bool                   `json:"stock_may_be_stale"`
	// The original multi-position order used the product-only fallback. Freeze
	// that fact before adding variants, so later checks do not rewrite history.
	MultipleOrderProductOnly bool        `json:"multiple_order_product_only,omitempty"`
	Operations               []Operation `json:"operations"`
}

// NewNamespace uses cryptographic randomness to make collisions unlikely; a
// remote uniqueness check is still required before any creation request.
func NewNamespace(now time.Time) (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate namespace: %w", err)
	}
	return "STYK-IT-" + now.UTC().Format("20060102") + "-" + hex.EncodeToString(b[:]), nil
}

// Plan is the exact supported fixture set, not a source of arbitrary requests.
func Plan(namespace string) ([]Operation, error) {
	if !namespacePattern.MatchString(namespace) {
		return nil, errors.New("invalid fixture namespace")
	}
	var out []Operation
	add := func(kind, key string) {
		out = append(out, Operation{Key: key, Kind: kind, Identity: namespace + "-" + key, State: Planned})
	}
	add("store", "warehouse-a")
	add("store", "warehouse-b")
	for _, key := range []string{"product-a", "product-b", "product-c", "product-d", "product-e", "duplicate-a", "duplicate-b", "no-article", "variant-parent"} {
		add("product", key)
	}
	add("characteristic", "variant-characteristic")
	add("variant", "variant-one")
	add("variant", "variant-two")
	add("counterparty", "counterparty")
	add("enter", "stock-a")
	add("enter", "stock-b")
	add("customerorder", "order-reserved")
	add("customerorder", "order-zero-reserve")
	add("customerorder", "order-multiple")
	add("customerorder", "order-partial")
	add("demand", "shipment-partial")
	return out, nil
}

// VariantExtension appends new documents; it never edits existing documents.
func VariantExtension(namespace string) ([]Operation, error) {
	if !namespacePattern.MatchString(namespace) {
		return nil, errors.New("invalid fixture namespace")
	}
	return []Operation{
		{Key: "stock-variants", Kind: "enter", Identity: namespace + "-stock-variants", State: Planned},
		{Key: "order-variant", Kind: "customerorder", Identity: namespace + "-order-variant", State: Planned},
	}, nil
}
