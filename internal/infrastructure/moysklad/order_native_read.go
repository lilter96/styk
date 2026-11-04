package moysklad

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type nativeDocumentDTO struct {
	orderNative
	CustomerOrder catalogReference `json:"customerOrder"`
	Demand        catalogReference `json:"demand"`
}

type nativeDocumentRead struct {
	observation orders.NativeDocumentObservation
	raw         map[string]json.RawMessage
	rows        []orderPositionRead
}

func (c *OrderClient) ReadNativeDocument(ctx context.Context, request orders.NativeDocumentRequest) (orders.NativeDocumentObservation, error) {
	if !c.scopeAllowed(request.Scope) || request.Correlation.Validate() != nil {
		return orders.NativeDocumentObservation{}, ErrOrderIdentity
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx); err != nil {
		return orders.NativeDocumentObservation{}, err
	}
	correlation := request.Correlation
	if correlation.DocumentID == "" {
		found, err := c.lookupNativeDocument(ctx, correlation)
		if err != nil {
			return orders.NativeDocumentObservation{}, err
		}
		correlation = found
	}
	read, err := c.readNativeDocument(ctx, correlation)
	if err != nil {
		return orders.NativeDocumentObservation{}, err
	}
	if request.IncludeReturns {
		if correlation.Kind != "demand" {
			return orders.NativeDocumentObservation{}, orders.ErrInvalid
		}
		read.observation, err = c.readNativeReturnCoverage(ctx, read)
	}
	if err == nil {
		err = read.observation.Validate()
	}
	return read.observation, err
}

func (c *OrderClient) lookupNativeDocument(ctx context.Context, correlation orders.NativeDocumentCorrelation) (orders.NativeDocumentCorrelation, error) {
	query := url.Values{"limit": {"2"}, "offset": {"0"}, "filter": {"syncId=" + correlation.SyncID + ";externalCode=" + correlation.Marker}}
	raw, err := c.client.get(ctx, "entity/"+correlation.Kind, query)
	if err != nil {
		return correlation, err
	}
	var collection page
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &collection) != nil || collection.Meta == nil || collection.Rows == nil || collection.Meta.Size == nil || collection.Meta.Offset == nil || collection.Meta.Limit == nil {
		return correlation, ErrOrderSchema
	}
	if *collection.Meta.Offset != 0 || *collection.Meta.Limit != 2 || *collection.Meta.Size != len(*collection.Rows) || len(*collection.Rows) > 1 {
		return correlation, orders.ErrConflict
	}
	if len(*collection.Rows) == 0 {
		return correlation, orders.ErrNotFound
	}
	document, _, err := c.decodeNativeIdentity((*collection.Rows)[0], correlation)
	if err == nil {
		correlation.DocumentID = document.ID
	}
	return correlation, err
}

func (c *OrderClient) decodeNativeIdentity(raw []byte, correlation orders.NativeDocumentCorrelation) (nativeDocumentDTO, map[string]json.RawMessage, error) {
	var document nativeDocumentDTO
	var fields map[string]json.RawMessage
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &document) != nil || json.Unmarshal(raw, &fields) != nil {
		return document, nil, ErrOrderSchema
	}
	id, err := c.referenceID(document.catalogReference, "entity/"+correlation.Kind, correlation.Kind)
	if err != nil || id != document.ID || document.AccountID != c.scope.MoyskladAccount || document.SyncID != correlation.SyncID || document.ExternalCode != correlation.Marker || correlation.DocumentID != "" && correlation.DocumentID != id {
		return document, nil, ErrOrderIdentity
	}
	return document, fields, nil
}

func (c *OrderClient) readNativeDocument(ctx context.Context, correlation orders.NativeDocumentCorrelation) (nativeDocumentRead, error) {
	path := "entity/" + correlation.Kind + "/" + correlation.DocumentID
	raw, err := c.client.get(ctx, path, nil)
	if err != nil {
		return nativeDocumentRead{}, err
	}
	document, fields, err := c.decodeNativeIdentity(raw, correlation)
	if err != nil {
		return nativeDocumentRead{}, err
	}
	observation, err := c.decodeNativeDocumentFields(ctx, document, fields, correlation)
	if err != nil {
		return nativeDocumentRead{}, err
	}
	rows, positions, err := c.readNativePositions(ctx, document, correlation.Kind, observation.Sum.Value.Currency)
	if err != nil {
		return nativeDocumentRead{}, err
	}
	observation.Positions = positions
	return c.finishNativeDocument(ctx, path, fields, rows, observation)
}

func (c *OrderClient) finishNativeDocument(ctx context.Context, path string, first map[string]json.RawMessage, rows []orderPositionRead, observation orders.NativeDocumentObservation) (nativeDocumentRead, error) {
	raw, err := c.client.get(ctx, path, nil)
	if err != nil {
		return nativeDocumentRead{}, err
	}
	document, last, err := c.decodeNativeIdentity(raw, observation.Correlation)
	if err != nil || document.Positions.Meta.Size == nil || *document.Positions.Meta.Size != len(rows) {
		return nativeDocumentRead{}, orders.ErrStale
	}
	before, err := orderCanonicalJSON(first)
	if err != nil {
		return nativeDocumentRead{}, err
	}
	after, err := orderCanonicalJSON(last)
	if err != nil || string(before) != string(after) {
		return nativeDocumentRead{}, orders.ErrStale
	}
	observation.Revision, err = orderTargetRevision(last, rows)
	if err != nil {
		return nativeDocumentRead{}, err
	}
	observation.Complete, observation.ObservedAt = true, time.Now().UTC()
	return nativeDocumentRead{observation: observation, raw: last, rows: rows}, nil
}
