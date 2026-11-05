package ordermoney

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/lilter96/styk/internal/orders"
)

type nativeConsumptionHistory struct {
	seal    orders.NativeConsumptionSeal
	demands map[string][]orders.Decimal
	works   map[string]orders.NativeConsumptionEvent
	docs    map[string]bool
	keys    []nativeConsumptionIdentity
}

type nativeConsumptionIdentity struct {
	WorkID, Fingerprint, DocumentID, DocumentRevision string
	State, ReceiptState                               orders.State
	DispatchCount, HTTPStatus                         int
	ReceiptCode                                       string
}

// PlanNativeConsumption uses the existing exact movement arithmetic. Its seal
// is local durable consumption, never evidence of current provider application
// or inclusion of a document in a stock report.
func PlanNativeConsumption(c orders.Command, f orders.NativeConsumptionFacts) (orders.NativeConsumptionSeal, orders.Validation) {
	h, issue := readNativeConsumption(c, f)
	if issue.Code != "" {
		return h.seal, issue
	}
	if issue := h.capacity(c); issue.Code != "" {
		return h.seal, issue
	}
	if err := h.sealDigest(c); err != nil || h.seal.Validate() != nil {
		return h.seal, consumptionIssue("native_consumption_seal_invalid")
	}
	return h.seal, orders.Validation{}
}

// NativeConsumptionSnapshot validates historical gross consumption without
// requesting the already-completed movement a second time. It is not fresh
// provider evidence and must never replace document points for stock eligibility.
func NativeConsumptionSnapshot(c orders.Command, f orders.NativeConsumptionFacts) (orders.NativeConsumptionSeal, orders.Validation) {
	h, issue := readNativeConsumption(c, f)
	if issue.Code != "" {
		return h.seal, issue
	}
	if err := h.sealDigest(c); err != nil || h.seal.Validate() != nil {
		return h.seal, consumptionIssue("native_consumption_seal_invalid")
	}
	return h.seal, orders.Validation{}
}

func readNativeConsumption(c orders.Command, f orders.NativeConsumptionFacts) (nativeConsumptionHistory, orders.Validation) {
	h := nativeConsumptionHistory{seal: orders.NativeConsumptionSeal{Positions: []orders.NativeConsumedPosition{}, Events: []orders.NativeConsumedEvent{}}, demands: make(map[string][]orders.Decimal), works: make(map[string]orders.NativeConsumptionEvent, len(f.Events)), docs: make(map[string]bool), keys: make([]nativeConsumptionIdentity, 0, len(f.Events))}
	if c.NativeMovement == nil || !c.Action.IsNativeMovement() || !f.Complete || f.CheckedAt.IsZero() || f.Events == nil || len(f.Events) > orders.MaxNativeConsumptionEvents || f.Scope != c.Scope || f.Correlation != c.Correlation || c.Correlation.CustomerOrderID == "" || c.NativeMovement.SourceBaseline.Validate() != nil || c.NativeMovement.SourceBaseline.Scope != c.Scope || c.NativeMovement.SourceBaseline.Correlation != c.Correlation {
		return h, consumptionIssue("native_consumption_coverage_unproven")
	}
	if issue := h.read(c, f); issue.Code != "" {
		return h, issue
	}
	return h, h.positions(c)
}

func NativeConsumptionAllows(c orders.Command, f orders.NativeConsumptionFacts) orders.Validation {
	seal, issue := PlanNativeConsumption(c, f)
	if issue.Code != "" {
		return issue
	}
	if c.NativeMovement.Consumption == nil {
		if c.NativeMovement.ParentEffect == orders.NativeParentUnpostedDocument {
			return consumptionIssue("native_consumption_seal_required")
		}
		return orders.Validation{} // Historical counter-mode commands stay readable.
	}
	actual, err := json.Marshal(c.NativeMovement.Consumption)
	expected, expectedErr := json.Marshal(seal)
	if err != nil || expectedErr != nil || string(actual) != string(expected) {
		return consumptionIssue("native_consumption_changed")
	}
	return orders.Validation{}
}

func (h *nativeConsumptionHistory) sealDigest(c orders.Command) error {
	sort.Slice(h.keys, func(i, j int) bool { return h.keys[i].WorkID < h.keys[j].WorkID })
	sort.Slice(h.seal.Events, func(i, j int) bool { return h.seal.Events[i].WorkID < h.seal.Events[j].WorkID })
	raw, err := json.Marshal(struct {
		Scope       orders.Scope
		Correlation orders.Correlation
		History     []nativeConsumptionIdentity
		Positions   []orders.NativeConsumedPosition
		Events      []orders.NativeConsumedEvent
	}{c.Scope, c.Correlation, h.keys, h.seal.Positions, h.seal.Events})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	h.seal.Digest = hex.EncodeToString(digest[:])
	return nil
}

func consumptionIssue(code string) orders.Validation {
	return orders.Validation{Field: "native_documents.consumption", Code: code}
}
