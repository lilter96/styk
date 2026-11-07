package ozon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
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
	snapshot, ready, err := c.discoverCarriageMethod(ctx, scope, input)
	if err != nil {
		return snapshot, err
	}
	if ready > 0 && len(snapshot.Blockers) == 0 && len(snapshot.CandidateIDs) == 0 {
		if err = c.collectCarriageReady(ctx, &snapshot, ready); err != nil {
			return snapshot, err
		}
	}
	snapshot.Fingerprint, err = orders.CarriageSnapshotFingerprint(snapshot)
	return snapshot, err
}

type carriageDiscovery struct {
	snapshot orders.CarriageSnapshot
	digest   hash.Hash
	matched  bool
}

func (c *OrdersClient) discoverCarriageMethod(ctx context.Context, scope orders.Scope, input orders.CarriageInput) (orders.CarriageSnapshot, int, error) {
	d := carriageDiscovery{snapshot: c.emptyCarriageDiscovery(scope, input), digest: sha256.New()}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		current, next, raw, err := c.carriageDiscoveryPage(ctx, scope, input, cursor)
		if err != nil {
			return d.snapshot, 0, err
		}
		if err = d.append(current, raw); err != nil {
			return d.snapshot, 0, err
		}
		if next == "" {
			return d.finish()
		}
		if seen[next] {
			return d.snapshot, 0, orders.ErrSchemaDrift
		}
		cursor = next
		seen[cursor] = true
	}
	return d.snapshot, 0, &LimitError{Resource: "carriage_discovery_pages"}
}

func (c *OrdersClient) emptyCarriageDiscovery(scope orders.Scope, input orders.CarriageInput) orders.CarriageSnapshot {
	return orders.CarriageSnapshot{Scope: scope, DeliveryMethodID: input.DeliveryMethodID, Members: []orders.CarriageMember{}, CandidateIDs: []string{}, Blockers: []orders.Validation{}, ObservedAt: c.now(), ContractVersion: "ozon-carriage-v2-primary-2026-11-07"}
}

func (c *OrdersClient) carriageDiscoveryPage(ctx context.Context, scope orders.Scope, input orders.CarriageInput, cursor string) (orders.CarriageSnapshot, string, []byte, error) {
	body := map[string]any{"limit": 1000}
	if cursor != "" {
		body["cursor"] = cursor
	}
	if input.DepartureDate != "" {
		body["filter"] = map[string]any{"delivery_method_id": jsonCarriageID(input.DeliveryMethodID), "departure_date": input.DepartureDate[:10]}
	}
	data, err := c.orderPost(ctx, carriageDiscoveryPath, body)
	if err != nil {
		return orders.CarriageSnapshot{}, "", nil, err
	}
	current, err := c.decodeCarriageDiscovery(data, scope, input)
	if err != nil {
		return current, "", nil, err
	}
	f := newOrderFields(data)
	more := f.boolean("has_next")
	next, err := orderString(f.raw["cursor"])
	if err != nil || !more.IsKnown() || (*more.Value && next == "") || (!*more.Value && next != "") {
		return current, "", nil, orders.ErrSchemaDrift
	}
	return current, next, data, nil
}

func (d *carriageDiscovery) append(current orders.CarriageSnapshot, raw []byte) error {
	if _, err := d.digest.Write(raw); err != nil {
		return err
	}
	d.snapshot.ObservedMethodCount += current.ObservedMethodCount
	if current.WarehouseID == "" {
		return nil
	}
	if d.matched {
		return orders.ErrSchemaDrift
	}
	d.matched = true
	d.snapshot.WarehouseID = current.WarehouseID
	d.snapshot.CandidateIDs = current.CandidateIDs
	d.snapshot.Blockers = current.Blockers
	d.snapshot.ReadyCount = current.ReadyCount
	return nil
}

func (d *carriageDiscovery) finish() (orders.CarriageSnapshot, int, error) {
	d.snapshot.Complete = true
	d.snapshot.SourceShapeFingerprint = hex.EncodeToString(d.digest.Sum(nil))
	if !d.matched || d.snapshot.ReadyCount == 0 {
		d.snapshot.Blockers = append(d.snapshot.Blockers, orders.Validation{Code: "CARRIAGE_NO_AVAILABLE_BATCH"})
	}
	return d.snapshot, d.snapshot.ReadyCount, nil
}

func (c *OrdersClient) decodeCarriageDiscovery(data []byte, scope orders.Scope, input orders.CarriageInput) (orders.CarriageSnapshot, error) {
	snapshot := c.emptyCarriageDiscovery(scope, input)
	if ordercontract.CheckJSON(data) != nil {
		return snapshot, orders.ErrSchemaDrift
	}
	f := newOrderFields(data)
	if !carriageKeys(f, "methods", "cursor", "has_next") || !carriageArrayPresent(f, "methods") {
		return snapshot, orders.ErrSchemaDrift
	}
	rows := f.array("methods")
	next := f.boolean("has_next")
	cursor, err := orderString(f.raw["cursor"])
	if err != nil || !next.IsKnown() || f.raw["cursor"] == nil || string(f.raw["cursor"]) == "null" {
		return snapshot, orders.ErrSchemaDrift
	}
	snapshot.ObservedMethodCount = len(rows)
	for _, row := range rows {
		method, err := decodeCarriageMethod(row)
		if err != nil {
			return snapshot, err
		}
		if method.id != input.DeliveryMethodID {
			continue
		}
		if input.DepartureDate != "" && method.date != input.DepartureDate[:10] {
			continue
		}
		if snapshot.WarehouseID != "" {
			return snapshot, orders.ErrSchemaDrift
		}
		snapshot.WarehouseID, snapshot.CandidateIDs, snapshot.Blockers, snapshot.ReadyCount = method.warehouse, method.candidates, method.blockers, method.ready
	}
	if *f.err != nil {
		return snapshot, orders.ErrSchemaDrift
	}
	snapshot.Complete = !*next.Value && cursor == ""
	if len(rows) == 0 && snapshot.Complete {
		snapshot.Blockers = append(snapshot.Blockers, orders.Validation{Code: "CARRIAGE_NO_AVAILABLE_BATCH"})
	}
	digest := sha256.Sum256(data)
	snapshot.SourceShapeFingerprint = hex.EncodeToString(digest[:])
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
