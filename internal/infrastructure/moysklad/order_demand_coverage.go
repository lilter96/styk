package moysklad

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

const nativeDemandPageSize = 1000

type nativeDemandLink struct{ ID, Parent string }

// ReadNativeDemandCoverage is deliberately separate from ordinary order reads:
// a missing relationship needs complete account visibility, not an empty header.
func (c *OrderClient) ReadNativeDemandCoverage(ctx context.Context, parent orders.TargetObservation) (orders.NativeDemandCoverage, error) {
	if !c.scopeAllowed(parent.Scope) || !c.validCorrelation(parent.Correlation) || parent.Correlation.CustomerOrderID == "" || !parent.Complete || parent.Revision == "" {
		return orders.NativeDemandCoverage{}, ErrOrderIdentity
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	return c.readNativeDemandCoverage(ctx, parent)
}

func (c *OrderClient) readNativeDemandCoverage(ctx context.Context, parent orders.TargetObservation) (orders.NativeDemandCoverage, error) {
	result := orders.NativeDemandCoverage{Scope: parent.Scope, Correlation: parent.Correlation, ParentRevision: parent.Revision, StartedAt: time.Now().UTC(), DemandIDs: make([]string, 0)}
	if err := c.nativeDemandVisibility(ctx); err != nil {
		return result, err
	}
	first, err := c.nativeDemandLinks(ctx)
	if err != nil {
		return result, err
	}
	second, err := c.nativeDemandLinks(ctx)
	if err != nil {
		return result, err
	}
	if !slices.Equal(first, second) {
		return result, orders.ErrStale
	}
	if err = c.nativeDemandVisibility(ctx); err != nil {
		return result, err
	}
	current, err := c.readCustomerOrder(ctx, parent.Correlation)
	if err != nil {
		return result, err
	}
	if !current.Complete || current.Revision != parent.Revision {
		return result, orders.ErrStale
	}
	for _, link := range second {
		if link.Parent == parent.Correlation.CustomerOrderID {
			result.DemandIDs = append(result.DemandIDs, link.ID)
		}
	}
	result.AccountRows, result.Visibility, result.Complete = len(second), "ALL", true
	result.FinishedAt = time.Now().UTC()
	return result, nil
}

func (c *OrderClient) nativeDemandVisibility(ctx context.Context) error {
	raw, err := c.client.get(ctx, "context/employee", nil)
	if err != nil {
		return err
	}
	var employee struct {
		AccountID   string `json:"accountId"`
		Permissions struct {
			Demand struct {
				View string `json:"view"`
			} `json:"demand"`
		} `json:"permissions"`
	}
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &employee) != nil || employee.AccountID != c.scope.MoyskladAccount {
		return ErrOrderIdentity
	}
	if employee.Permissions.Demand.View != "ALL" {
		return orders.ErrBlocked
	}
	return nil
}

func (c *OrderClient) nativeDemandLinks(ctx context.Context) ([]nativeDemandLink, error) {
	links := make([]nativeDemandLink, 0)
	total := -1
	for offset := 0; total < 0 || offset < total; {
		rows, size, err := c.nativeDemandPage(ctx, offset, total)
		if err != nil {
			return nil, err
		}
		total = size
		for _, raw := range rows {
			link, err := c.nativeDemandLink(raw)
			if err != nil {
				return nil, err
			}
			if len(links) > 0 && links[len(links)-1].ID >= link.ID {
				return nil, orders.ErrConflict
			}
			links = append(links, link)
		}
		offset += len(rows)
	}
	return links, nil
}

func (c *OrderClient) nativeDemandPage(ctx context.Context, offset, total int) ([]json.RawMessage, int, error) {
	query := url.Values{"order": {"id,asc"}, "limit": {strconv.Itoa(nativeDemandPageSize)}, "offset": {strconv.Itoa(offset)}}
	raw, err := c.client.get(ctx, "entity/demand", query)
	if err != nil {
		return nil, 0, err
	}
	var collection page
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &collection) != nil || collection.Meta == nil || collection.Rows == nil || collection.Meta.Size == nil || collection.Meta.Offset == nil || collection.Meta.Limit == nil {
		return nil, 0, ErrOrderSchema
	}
	m := collection.Meta
	if *m.Offset != offset || *m.Limit != nativeDemandPageSize || *m.Size < offset || *m.Size > maxRows || total >= 0 && *m.Size != total || len(*collection.Rows) != min(nativeDemandPageSize, *m.Size-offset) {
		return nil, 0, ErrOrderSchema
	}
	return *collection.Rows, *m.Size, nil
}

func (c *OrderClient) nativeDemandLink(raw json.RawMessage) (nativeDemandLink, error) {
	var document nativeDocumentDTO
	var fields map[string]json.RawMessage
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &document) != nil || json.Unmarshal(raw, &fields) != nil {
		return nativeDemandLink{}, ErrOrderSchema
	}
	id, err := c.referenceID(document.catalogReference, "entity/demand", "demand")
	if err != nil || id != document.ID || document.AccountID != c.scope.MoyskladAccount {
		return nativeDemandLink{}, ErrOrderIdentity
	}
	link := nativeDemandLink{ID: id}
	if value, exists := fields["customerOrder"]; !exists || string(value) == "null" {
		return link, nil
	}
	link.Parent, err = c.referenceID(document.CustomerOrder, "entity/customerorder", "customerorder")
	return link, err
}
