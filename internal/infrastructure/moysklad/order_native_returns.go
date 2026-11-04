package moysklad

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

const maxNativeReturnRows = 1000
const maxNativeRelevantReturns = 25

// No creation-time window is used: older partial returns must not disappear.
// Limits are explicit blockers, never a declaration of zero returned quantity.
func (c *OrderClient) readNativeReturnCoverage(ctx context.Context, basis nativeDocumentRead) (orders.NativeDocumentObservation, error) {
	result := basis.observation
	result.ReturnsStartedAt = time.Now().UTC()
	result.Returns = make([]orders.NativeReturnEvidence, 0)
	seen := make(map[string]bool)
	err := c.client.pages(ctx, "entity/salesreturn", url.Values{"order": {"id"}}, func(raw json.RawMessage) error { return c.collectNativeReturn(ctx, raw, &result, seen) })
	if err != nil {
		return result, err
	}
	if err = c.nativeReturnBasisUnchanged(ctx, basis); err != nil {
		return result, err
	}
	result.ReturnsComplete, result.ReturnsFinishedAt = true, time.Now().UTC()
	return result, nil
}

func (c *OrderClient) collectNativeReturn(ctx context.Context, raw json.RawMessage, result *orders.NativeDocumentObservation, seen map[string]bool) error {
	if len(seen) == maxNativeReturnRows {
		return orders.ErrBlocked
	}
	document, fields, err := c.nativeReturnListIdentity(raw)
	if err != nil {
		return err
	}
	if seen[document.ID] {
		return orders.ErrConflict
	}
	seen[document.ID] = true
	if _, present := fields["demand"]; !present || string(fields["demand"]) == "null" {
		return nil
	}
	demand, err := c.referenceID(document.Demand, "entity/demand", "demand")
	if err != nil {
		return err
	}
	if demand != result.Correlation.DocumentID {
		return nil
	}
	if len(result.Returns) == maxNativeRelevantReturns {
		return orders.ErrBlocked
	}
	returned, err := c.readNativeDocument(ctx, orders.NativeDocumentCorrelation{Kind: "salesreturn", DocumentID: document.ID, SyncID: document.SyncID, Marker: document.ExternalCode})
	if err != nil {
		return err
	}
	observation := returned.observation
	if !observation.DemandID.IsKnown() || *observation.DemandID.Value != demand || observation.OrganizationID != result.OrganizationID || observation.AgentID != result.AgentID || observation.CurrencyID != result.CurrencyID {
		return orders.ErrStale
	}
	evidence := orders.NativeReturnEvidence{ID: document.ID, DemandID: demand, Applicable: observation.Applicable, Positions: observation.Positions, Revision: observation.Revision, ObservedAt: observation.ObservedAt}
	if err = evidence.Validate(demand, result.Sum.Value.Currency); err != nil {
		return err
	}
	result.Returns = append(result.Returns, evidence)
	return nil
}

func (c *OrderClient) nativeReturnListIdentity(raw []byte) (nativeDocumentDTO, map[string]json.RawMessage, error) {
	var document nativeDocumentDTO
	var fields map[string]json.RawMessage
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &document) != nil || json.Unmarshal(raw, &fields) != nil {
		return document, nil, ErrOrderSchema
	}
	id, err := c.referenceID(document.catalogReference, "entity/salesreturn", "salesreturn")
	if err != nil || document.ID != id || document.AccountID != c.scope.MoyskladAccount {
		return document, nil, ErrOrderIdentity
	}
	return document, fields, nil
}

func (c *OrderClient) nativeReturnBasisUnchanged(ctx context.Context, basis nativeDocumentRead) error {
	path := "entity/demand/" + basis.observation.Correlation.DocumentID
	raw, err := c.client.get(ctx, path, nil)
	if err != nil {
		return err
	}
	_, fields, err := c.decodeNativeIdentity(raw, basis.observation.Correlation)
	if err != nil {
		return err
	}
	first, err := orderCanonicalJSON(basis.raw)
	if err != nil {
		return err
	}
	last, err := orderCanonicalJSON(fields)
	if err != nil || string(first) != string(last) {
		return orders.ErrStale
	}
	return nil
}

// NativeReturnTraversalLimit is a non-PII diagnostic for the current bounded
// account traversal. It is not a provider quota or an asserted order watermark.
func NativeReturnTraversalLimit() string {
	return strconv.Itoa(maxNativeReturnRows) + " account returns / " + strconv.Itoa(maxNativeRelevantReturns) + " related returns"
}
