package moysklad

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"

	"github.com/lilter96/styk/internal/orders"
)

type orderNative struct {
	catalogReference
	ID           string           `json:"id"`
	AccountID    string           `json:"accountId"`
	SyncID       string           `json:"syncId"`
	ExternalCode string           `json:"externalCode"`
	Updated      string           `json:"updated"`
	Organization catalogReference `json:"organization"`
	Agent        catalogReference `json:"agent"`
	Store        catalogReference `json:"store"`
	State        catalogReference `json:"state"`
	SalesChannel catalogReference `json:"salesChannel"`
	Rate         struct {
		Currency catalogReference `json:"currency"`
		Value    json.RawMessage  `json:"value"`
	} `json:"rate"`
	Positions struct {
		Meta struct {
			Href string `json:"href"`
			Type string `json:"type"`
			Size *int   `json:"size"`
		} `json:"meta"`
	} `json:"positions"`
}

func (c *OrderClient) ReadOrderCorrelation(ctx context.Context, correlation orders.Correlation) (orders.Correlation, error) {
	if !c.validCorrelation(correlation) {
		return orders.Correlation{}, ErrOrderIdentity
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx); err != nil {
		return orders.Correlation{}, err
	}
	return c.readOrderCorrelation(ctx, correlation)
}

func (c *OrderClient) validCorrelation(correlation orders.Correlation) bool {
	return c.scopeAllowed(correlation.Scope) && correlation.Validate() == nil && (correlation.CustomerOrderID == "" || orderUUID.MatchString(correlation.CustomerOrderID))
}

func (c *OrderClient) readOrderCorrelation(ctx context.Context, correlation orders.Correlation) (orders.Correlation, error) {
	query := url.Values{"limit": {"2"}, "offset": {"0"}, "filter": {"syncId=" + correlation.SyncID + ";externalCode=" + correlation.Marker}}
	raw, err := c.client.get(ctx, "entity/customerorder", query)
	if err != nil {
		return orders.Correlation{}, err
	}
	var collection page
	if json.Unmarshal(raw, &collection) != nil || collection.Meta == nil || collection.Rows == nil || collection.Meta.Size == nil || collection.Meta.Offset == nil || collection.Meta.Limit == nil {
		return orders.Correlation{}, ErrOrderSchema
	}
	if *collection.Meta.Offset != 0 || *collection.Meta.Limit != 2 || *collection.Meta.Size != len(*collection.Rows) || len(*collection.Rows) > 1 {
		return orders.Correlation{}, orders.ErrConflict
	}
	if len(*collection.Rows) == 0 {
		// An eventually consistent zero-row read cannot prove non-creation.
		return orders.Correlation{}, ErrOrderCorrelation
	}
	document, _, err := c.decodeOrderIdentity((*collection.Rows)[0], correlation)
	if err != nil {
		return orders.Correlation{}, err
	}
	correlation.CustomerOrderID = document.ID
	return correlation, nil
}

func (c *OrderClient) decodeOrderIdentity(raw []byte, correlation orders.Correlation) (orderNative, map[string]json.RawMessage, error) {
	var document orderNative
	var fields map[string]json.RawMessage
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &document) != nil || json.Unmarshal(raw, &fields) != nil {
		return orderNative{}, nil, ErrOrderSchema
	}
	id, err := c.referenceID(document.catalogReference, "entity/customerorder", "customerorder")
	if err != nil || document.ID != id || document.AccountID != c.scope.MoyskladAccount || document.SyncID != correlation.SyncID || document.ExternalCode != correlation.Marker {
		return orderNative{}, nil, ErrOrderIdentity
	}
	if correlation.CustomerOrderID != "" && document.ID != correlation.CustomerOrderID {
		return orderNative{}, nil, ErrOrderIdentity
	}
	return document, fields, nil
}

func (c *OrderClient) ReadCustomerOrder(ctx context.Context, correlation orders.Correlation) (orders.TargetObservation, error) {
	if !c.validCorrelation(correlation) {
		return orders.TargetObservation{}, ErrOrderIdentity
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx); err != nil {
		return orders.TargetObservation{}, err
	}
	if correlation.CustomerOrderID == "" {
		found, err := c.readOrderCorrelation(ctx, correlation)
		if err != nil {
			return orders.TargetObservation{}, err
		}
		correlation = found
	}
	return c.readCustomerOrder(ctx, correlation)
}

func (c *OrderClient) readCustomerOrder(ctx context.Context, correlation orders.Correlation) (orders.TargetObservation, error) {
	path := "entity/customerorder/" + correlation.CustomerOrderID
	raw, err := c.client.get(ctx, path, nil)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	document, fields, err := c.decodeOrderIdentity(raw, correlation)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	observation, err := c.decodeOrderFields(ctx, document, fields, correlation)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	rows, err := c.readOrderPositions(ctx, document, observation.Fields.CurrencyID, observation.Sum)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	return c.finishOrderRead(ctx, path, fields, rows, observation)
}

type orderPositionRead struct {
	Target orders.TargetPosition
	Raw    json.RawMessage
}

func (c *OrderClient) readOrderPositions(ctx context.Context, document orderNative, currencyID string, sum orders.Field[orders.Money]) ([]orderPositionRead, error) {
	path := "entity/customerorder/" + document.ID + "/positions"
	if document.Positions.Meta.Href != c.client.base.ResolveReference(&url.URL{Path: path}).String() || document.Positions.Meta.Type != "customerorderposition" || document.Positions.Meta.Size == nil || *document.Positions.Meta.Size < 0 || currencyID == "" || !sum.IsKnown() {
		return nil, ErrOrderSchema
	}
	result := make([]orderPositionRead, 0)
	seen := make(map[string]bool)
	err := c.client.pages(ctx, path, nil, func(raw json.RawMessage) error {
		position, err := c.decodeOrderPosition(raw, sum.Value.Currency)
		if err != nil {
			return err
		}
		if seen[position.ID] {
			return orders.ErrConflict
		}
		seen[position.ID] = true
		result = append(result, orderPositionRead{Target: position, Raw: raw})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(result) != *document.Positions.Meta.Size {
		return nil, orders.ErrStale
	}
	return result, nil
}

func (c *OrderClient) finishOrderRead(ctx context.Context, path string, first map[string]json.RawMessage, rows []orderPositionRead, observation orders.TargetObservation) (orders.TargetObservation, error) {
	raw, err := c.client.get(ctx, path, nil)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	current, last, err := c.decodeOrderIdentity(raw, observation.Correlation)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	if current.Positions.Meta.Size == nil || *current.Positions.Meta.Size != len(rows) {
		return orders.TargetObservation{}, orders.ErrStale
	}
	delete(first, "positions")
	delete(last, "positions")
	before, err := orderCanonicalJSON(first)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	after, err := orderCanonicalJSON(last)
	if err != nil || !bytes.Equal(before, after) {
		return orders.TargetObservation{}, orders.ErrStale
	}
	revision, err := orderTargetRevision(last, rows)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	observation.Revision, observation.Complete = revision, true
	observation.Positions = make([]orders.TargetPosition, 0, len(rows))
	for _, row := range rows {
		observation.Positions = append(observation.Positions, row.Target)
	}
	return observation, nil
}

func orderCanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, ErrOrderSchema
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var canonical any
	if decoder.Decode(&canonical) != nil {
		return nil, ErrOrderSchema
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, ErrOrderSchema
	}
	return encoded, nil
}

func orderTargetRevision(document map[string]json.RawMessage, rows []orderPositionRead) (string, error) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Target.ID < rows[j].Target.ID })
	positions := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		positions = append(positions, row.Raw)
	}
	raw, err := orderCanonicalJSON(struct {
		Document  map[string]json.RawMessage `json:"document"`
		Positions []json.RawMessage          `json:"positions"`
	}{document, positions})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

var _ orders.TargetStatusWriter = (*OrderClient)(nil)
