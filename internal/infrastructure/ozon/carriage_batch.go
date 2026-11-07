package ozon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

const carriageDiscoveryPath = "/v2/carriage/delivery/list"

func (c *OrdersClient) ReadCarriageBatch(ctx context.Context, scope orders.Scope, input orders.CarriageInput) (orders.CarriageSnapshot, error) {
	if scope != c.scope || input.Validate() != nil {
		return orders.CarriageSnapshot{}, orders.ErrInvalid
	}
	if input.Action == orders.CarriageApprove {
		return c.ReadCarriage(ctx, scope, input.CarriageID)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return c.readCarriageDiscovery(ctx, scope, input)
}

func (c *OrdersClient) readCarriageDiscovery(ctx context.Context, scope orders.Scope, input orders.CarriageInput) (orders.CarriageSnapshot, error) {
	var result orders.CarriageSnapshot
	var cursor string
	seen := make(map[string]bool, 10)
	pageHashes := make([]string, 0, 10)
	total := 0
	for page := 0; page < 10; page++ {
		body := map[string]any{"limit": 1000}
		if cursor != "" {
			body["cursor"] = cursor
		}
		data, err := c.orderPost(ctx, carriageDiscoveryPath, body)
		if err != nil {
			return result, err
		}
		current, err := c.decodeCarriageDiscovery(data, scope, input)
		if err != nil {
			return result, err
		}
		total += current.ObservedMethodCount
		pageHashes = append(pageHashes, current.SourceShapeFingerprint)
		result = current
		f := newOrderFields(data)
		next := f.boolean("has_next")
		cursor, err = orderString(f.raw["cursor"])
		if err != nil || *f.err != nil {
			return result, orders.ErrSchemaDrift
		}
		if !*next.Value {
			return c.finishCarriageDiscovery(result, total, pageHashes)
		}
		if cursor == "" || seen[cursor] {
			return result, orders.ErrSchemaDrift
		}
		seen[cursor] = true
	}
	return result, &LimitError{Resource: "carriage_discovery_pages"}
}

func (c *OrdersClient) finishCarriageDiscovery(result orders.CarriageSnapshot, total int, hashes []string) (orders.CarriageSnapshot, error) {
	result.ObservedMethodCount = total
	joined := sha256.New()
	for _, hash := range hashes {
		if _, err := joined.Write([]byte(hash)); err != nil {
			return result, err
		}
	}
	result.SourceShapeFingerprint = hex.EncodeToString(joined.Sum(nil))
	result.Complete = total == 0 && result.Complete
	if total > 0 {
		result.Blockers = []orders.Validation{{Code: "CARRIAGE_NONEMPTY_DISCOVERY_CONTRACT_UNVERIFIED"}}
	}
	var err error
	result.Fingerprint, err = orders.CarriageSnapshotFingerprint(result)
	return result, err
}

// The authenticated production API supplies this exact empty envelope. Unknown
// nonempty method rows are not projected into guessed membership or authority.
func (c *OrdersClient) decodeCarriageDiscovery(data []byte, scope orders.Scope, input orders.CarriageInput) (orders.CarriageSnapshot, error) {
	if ordercontract.CheckJSON(data) != nil {
		return orders.CarriageSnapshot{}, orders.ErrSchemaDrift
	}
	f := newOrderFields(data)
	if !carriageKeys(f, "methods", "cursor", "has_next") || f.raw["methods"] == nil || string(f.raw["methods"]) == "null" {
		return orders.CarriageSnapshot{}, orders.ErrSchemaDrift
	}
	methods := f.array("methods")
	cursor, cursorErr := orderString(f.raw["cursor"])
	next := f.boolean("has_next")
	if *f.err != nil || !next.IsKnown() || cursorErr != nil || f.raw["cursor"] == nil || string(f.raw["cursor"]) == "null" {
		return orders.CarriageSnapshot{}, orders.ErrSchemaDrift
	}
	snapshot := orders.CarriageSnapshot{Scope: scope, DeliveryMethodID: input.DeliveryMethodID, Members: []orders.CarriageMember{}, CandidateIDs: []string{}, Blockers: []orders.Validation{}, ObservedAt: c.now(), ContractVersion: "ozon-carriage-v2-live-empty-2026-11-07"}
	sourceHash := sha256.Sum256(data)
	snapshot.SourceShapeFingerprint = hex.EncodeToString(sourceHash[:])
	snapshot.ObservedMethodCount = len(methods)
	if len(methods) > 0 {
		snapshot.Blockers = append(snapshot.Blockers, orders.Validation{Code: "CARRIAGE_NONEMPTY_DISCOVERY_CONTRACT_UNVERIFIED"})
	}
	if *next.Value || cursor != "" {
		snapshot.Blockers = append(snapshot.Blockers, orders.Validation{Code: "CARRIAGE_DISCOVERY_MORE_PAGES"})
	}
	snapshot.Complete = len(methods) == 0 && !*next.Value && cursor == ""
	if snapshot.Complete {
		snapshot.Blockers = append(snapshot.Blockers, orders.Validation{Code: "CARRIAGE_NO_AVAILABLE_BATCH"})
	}
	var err error
	snapshot.Fingerprint, err = orders.CarriageSnapshotFingerprint(snapshot)
	return snapshot, err
}

func carriageKeys(fields orderFields, allowed ...string) bool {
	for key := range fields.raw {
		if !slices.Contains(allowed, key) {
			return false
		}
	}
	return true
}
