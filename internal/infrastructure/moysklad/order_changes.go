package moysklad

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (c *OrderClient) ReadNativeOrderChanges(ctx context.Context, scope orders.Scope, from, to time.Time, limit int, cursor string) (orders.NativeOrderChanges, error) {
	if !c.scopeAllowed(scope) || from.IsZero() || !to.After(from) || to.Sub(from) > 31*24*time.Hour || limit < 1 || limit > orders.MaxPagePostings {
		return orders.NativeOrderChanges{}, orders.ErrInvalid
	}
	offset, err := nativeChangeOffset(cursor)
	if err != nil {
		return orders.NativeOrderChanges{}, err
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err = c.verifyAccount(ctx); err != nil {
		return orders.NativeOrderChanges{}, err
	}
	zone, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return orders.NativeOrderChanges{}, err
	}
	format := func(t time.Time) string { return t.In(zone).Format("2006-01-02 15:04:05.000") }
	query := url.Values{"filter": {"updated>=" + format(from) + ";updated<" + format(to)}, "order": {"updated,asc;id,asc"}, "limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	raw, err := c.client.get(ctx, "entity/customerorder", query)
	if err != nil {
		return orders.NativeOrderChanges{}, err
	}
	return decodeNativeChanges(raw, scope, offset, limit, from, to, zone)
}

func nativeChangeOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 || offset > 100000 || strconv.Itoa(offset) != cursor {
		return 0, orders.ErrInvalid
	}
	return offset, nil
}

func decodeNativeChanges(raw []byte, scope orders.Scope, offset, limit int, from, to time.Time, zone *time.Location) (orders.NativeOrderChanges, error) {
	var collection page
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &collection) != nil || collection.Meta == nil || collection.Rows == nil || collection.Meta.Size == nil || collection.Meta.Offset == nil || collection.Meta.Limit == nil {
		return orders.NativeOrderChanges{}, ErrOrderSchema
	}
	meta, rows := collection.Meta, *collection.Rows
	if *meta.Offset != offset || *meta.Limit != limit || len(rows) > limit || *meta.Size < 0 || *meta.Size > 100000 || offset+len(rows) > *meta.Size || len(rows) == 0 && offset < *meta.Size {
		return orders.NativeOrderChanges{}, ErrOrderSchema
	}
	result := orders.NativeOrderChanges{Changes: make([]orders.NativeOrderChange, 0, len(rows))}
	seen := make(map[string]bool, len(rows))
	previous := ""
	for _, rawRow := range rows {
		var row struct {
			ID      string `json:"id"`
			Account string `json:"accountId"`
			Updated string `json:"updated"`
		}
		if json.Unmarshal(rawRow, &row) != nil || !orderUUID.MatchString(row.ID) || row.Account != scope.MoyskladAccount || seen[row.ID] {
			return result, ErrOrderIdentity
		}
		stamp, err := time.ParseInLocation("2006-01-02 15:04:05", row.Updated, zone)
		key := stamp.UTC().Format("2006-01-02T15:04:05.000000000Z") + ":" + row.ID
		if err != nil || stamp.Before(from) || !stamp.Before(to) || previous != "" && key <= previous {
			return result, ErrOrderSchema
		}
		seen[row.ID], previous = true, key
		result.Changes = append(result.Changes, orders.NativeOrderChange{ID: row.ID, Updated: row.Updated})
	}
	if offset+len(rows) < *meta.Size {
		result.NextCursor = strconv.Itoa(offset + len(rows))
	}
	return result, nil
}
