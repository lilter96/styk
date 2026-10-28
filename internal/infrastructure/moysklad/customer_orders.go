package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata" // MoySklad timestamps use Moscow time even in minimal containers.

	"example.com/styk/internal/domain"
)

type customerOrderDTO struct {
	ID, AccountID                             string
	SyncID, Created, Updated, Deleted, Moment *string
	Applicable                                *bool
	Store, State                              *ref
}

type customerPositionDTO struct {
	ID, AccountID              string
	Assortment                 *ref
	Quantity, Reserve, Shipped *json.Number
	Pack                       *struct {
		ID       *string
		Quantity *json.Number
		UOM      *ref `json:"uom"`
	}
}

func refID(r *ref, kind string) (*string, error) {
	if r == nil {
		return nil, nil
	}
	if r.Meta.Type != kind {
		return nil, errors.New("unexpected reference type")
	}
	pathKind := kind
	if kind == "state" {
		pathKind = "states"
	}
	id, err := idFromHref(r.Meta.Href, pathKind)
	if err != nil {
		return nil, err
	}
	if !orderUUID.MatchString(id) {
		return nil, errors.New("invalid reference identifier")
	}
	return &id, nil
}

// ReadCustomerOrders is bounded by source updated time. Traversal does not
// prove visibility, deletion coverage, or a consistent point-in-time snapshot.
func (c *Client) ReadCustomerOrders(ctx context.Context, tenant, account string, since, until time.Time) (domain.CustomerOrderImport, error) {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(account) == "" || since.IsZero() || !until.After(since) || until.Sub(since) > 31*24*time.Hour || !since.Equal(since.Truncate(time.Millisecond)) || !until.Equal(until.Truncate(time.Millisecond)) {
		return domain.CustomerOrderImport{}, errors.New("tenant, account and a millisecond-aligned window of at most 31 days are required")
	}
	out := domain.CustomerOrderImport{Tenant: tenant, Account: account, Since: since.UTC(), Until: until.UTC(), StartedAt: time.Now().UTC()}
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return domain.CustomerOrderImport{}, errors.New("Moscow timezone is unavailable")
	}
	format := func(t time.Time) string { return t.In(msk).Format("2006-01-02 15:04:05.000") }
	query := url.Values{"filter": {"updated>=" + format(since) + ";updated<" + format(until)}, "order": {"id,asc"}}
	seen := map[string]bool{}
	positionsCount := 0
	err = c.pages(ctx, "entity/customerorder", query, func(raw json.RawMessage) error {
		var listed customerOrderDTO
		if err := json.Unmarshal(raw, &listed); err != nil {
			return errors.New("invalid customer order list record")
		}
		if listed.Updated != nil && strings.TrimSpace(*listed.Updated) == "" {
			listed.Updated = nil
		}
		path := "entity/customerorder/" + listed.ID
		if !orderReadPath.MatchString(path) || listed.AccountID != account || seen[strings.ToLower(listed.ID)] {
			return errors.New("missing, duplicate, invalid or cross-account customer order")
		}
		seen[strings.ToLower(listed.ID)] = true
		data, err := c.get(ctx, path, nil)
		if err != nil {
			return fmt.Errorf("read customer order: %w", err)
		}
		var detail customerOrderDTO
		if err = json.Unmarshal(data, &detail); err != nil {
			return errors.New("invalid customer order detail")
		}
		if detail.ID != listed.ID || detail.AccountID != account {
			return errors.New("inconsistent customer order identity or account")
		}
		if detail.SyncID != nil {
			if strings.TrimSpace(*detail.SyncID) == "" {
				detail.SyncID = nil
			} else if !orderUUID.MatchString(*detail.SyncID) {
				return errors.New("invalid customer order sync identity")
			}
		}
		for _, field := range []**string{&detail.Created, &detail.Updated, &detail.Deleted, &detail.Moment} {
			if *field == nil {
				continue
			}
			if strings.TrimSpace(**field) == "" {
				*field = nil
				continue
			}
			if _, err := time.Parse("2006-01-02 15:04:05.000", **field); err != nil {
				if _, err = time.Parse("2006-01-02 15:04:05", **field); err != nil {
					return errors.New("invalid customer order source timestamp")
				}
			}
		}
		if listed.Updated != nil && detail.Updated != nil && *listed.Updated != *detail.Updated {
			return errors.New("customer order changed during traversal")
		}
		warehouse, err := refID(detail.Store, "store")
		if err != nil {
			return fmt.Errorf("invalid warehouse reference: %w", err)
		}
		state, err := refID(detail.State, "state")
		if err != nil {
			return fmt.Errorf("invalid state reference: %w", err)
		}
		order := domain.CustomerOrderObservation{ID: detail.ID, Account: account, SyncID: detail.SyncID, Created: detail.Created, Updated: detail.Updated, Deleted: detail.Deleted, Moment: detail.Moment, Applicable: detail.Applicable, StateID: state, WarehouseID: warehouse, ObservedAt: time.Now().UTC()}
		positions := map[string]bool{}
		err = c.pages(ctx, path+"/positions", nil, func(raw json.RawMessage) error {
			var p customerPositionDTO
			if err := json.Unmarshal(raw, &p); err != nil {
				return errors.New("invalid customer order position")
			}
			if !orderUUID.MatchString(p.ID) || positions[strings.ToLower(p.ID)] || p.AccountID != account {
				return errors.New("missing, duplicate or cross-account position")
			}
			positions[strings.ToLower(p.ID)] = true
			positionsCount++
			if positionsCount > maxRows {
				return errors.New("customer order position limit exceeded")
			}
			v := domain.CustomerOrderPosition{ID: p.ID, Quantity: number(p.Quantity), Reserve: number(p.Reserve), Shipped: number(p.Shipped), ObservedAt: time.Now().UTC()}
			if p.Assortment != nil {
				v.AssortmentKind = p.Assortment.Meta.Type
				switch v.AssortmentKind {
				case "product", "variant", "service", "consignment", "bundle":
				default:
					return errors.New("unsupported assortment type")
				}
				v.AssortmentID, err = idFromHref(p.Assortment.Meta.Href, v.AssortmentKind)
				if err != nil {
					return errors.New("invalid assortment identity")
				}
				if !orderUUID.MatchString(v.AssortmentID) {
					return errors.New("invalid assortment identity")
				}
			}
			if p.Pack != nil {
				v.PackID, v.PackQuantity = p.Pack.ID, number(p.Pack.Quantity)
				if v.PackID != nil && !orderUUID.MatchString(*v.PackID) {
					return errors.New("invalid pack identity")
				}
				v.PackUnitID, err = refID(p.Pack.UOM, "uom")
				if err != nil {
					return errors.New("invalid pack unit identity")
				}
			}
			order.Positions = append(order.Positions, v)
			return nil
		})
		if err != nil {
			return fmt.Errorf("read customer order positions: %w", err)
		}
		out.Orders = append(out.Orders, order)
		return nil
	})
	if err != nil {
		return domain.CustomerOrderImport{}, fmt.Errorf("read customer orders: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return domain.CustomerOrderImport{}, err
	}
	out.FinishedAt = time.Now().UTC()
	return out, nil
}
