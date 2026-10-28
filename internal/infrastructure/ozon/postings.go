package ozon

import (
	"context"
	"errors"
	"example.com/styk/internal/domain"
	"fmt"
	"strings"
	"time"
)

type postingDTO struct {
	PostingNumber            string   `json:"posting_number"`
	OrderID                  *integer `json:"order_id"`
	OrderNumber              string   `json:"order_number"`
	Status                   string   `json:"status"`
	Substatus                string   `json:"substatus"`
	InProcessAt              string   `json:"in_process_at"`
	ShipmentDate             string   `json:"shipment_date"`
	DeliveringDate           *string  `json:"delivering_date"`
	FactDeliveryDate         *string  `json:"fact_delivery_date"`
	ShipmentDateWithoutDelay *string  `json:"shipment_date_without_delay"`
	ParentPostingNumber      string   `json:"parent_posting_number"`
	DeliveryMethod           *struct {
		WarehouseID *integer `json:"warehouse_id"`
	} `json:"delivery_method"`
	Cancellation *struct {
		Reason    *string  `json:"cancel_reason"`
		ReasonID  *integer `json:"cancel_reason_id"`
		Type      *string  `json:"cancellation_type"`
		Initiator *string  `json:"cancellation_initiator"`
		After     *bool    `json:"cancelled_after_ship"`
	} `json:"cancellation"`
	Products *[]struct {
		OfferID  string   `json:"offer_id"`
		SKU      *integer `json:"sku"`
		Quantity *int64   `json:"quantity"`
	} `json:"products"`
}

func (c *Client) ReadPostings(ctx context.Context, tenant string, since, until time.Time) (domain.PostingImport, error) {
	b, _ := ctx.Value(budgetKey{}).(*budget)
	if strings.TrimSpace(tenant) == "" || since.IsZero() || !until.After(since) || until.After(since.AddDate(1, 0, 0)) {
		return domain.PostingImport{}, errors.New("tenant and a bounded window of at most one year required")
	}
	out := domain.PostingImport{Tenant: tenant, Account: c.id, Since: since, Until: until, StartedAt: time.Now().UTC()}
	cursor := ""
	cursors := map[string]bool{}
	seen := map[string]bool{}
	for {
		var page struct {
			Postings *[]struct {
				PostingNumber string `json:"posting_number"`
			} `json:"postings"`
			HasNext *bool  `json:"has_next"`
			Cursor  string `json:"cursor"`
		}
		req := map[string]any{"filter": map[string]any{"since": since.UTC().Format(time.RFC3339Nano), "to": until.UTC().Format(time.RFC3339Nano)}, "limit": 100, "cursor": cursor, "sort_dir": "asc", "with": map[string]bool{"analytics_data": false, "financial_data": false, "legal_info": false, "barcodes": false}}
		if err := c.post(ctx, "/v4/posting/fbs/list", req, &page); err != nil {
			return domain.PostingImport{}, fmt.Errorf("list postings: %w", err)
		}
		if page.Postings == nil {
			return domain.PostingImport{}, errors.New("missing v4 postings")
		}
		for _, p := range *page.Postings {
			if b != nil {
				if err := b.add("postings", 1); err != nil {
					return domain.PostingImport{}, err
				}
				if err := b.add("observations", 1); err != nil {
					return domain.PostingImport{}, err
				}
			}
			if p.PostingNumber == "" || seen[p.PostingNumber] {
				return domain.PostingImport{}, errors.New("missing or duplicate posting")
			}
			seen[p.PostingNumber] = true
			var detail struct {
				Result *postingDTO `json:"result"`
			}
			if err := c.post(ctx, "/v3/posting/fbs/get", map[string]any{"posting_number": p.PostingNumber, "with": map[string]bool{"analytics_data": false, "financial_data": false, "legal_info": false, "barcodes": false}}, &detail); err != nil {
				return domain.PostingImport{}, fmt.Errorf("read posting detail: %w", err)
			}
			if detail.Result == nil || detail.Result.PostingNumber != p.PostingNumber {
				return domain.PostingImport{}, errors.New("missing or inconsistent posting detail")
			}
			x := detail.Result
			if id(x.OrderID) == "" || x.OrderNumber == "" || x.Status == "" || x.Products == nil {
				return domain.PostingImport{}, errors.New("incomplete posting detail")
			}
			o := domain.PostingObservation{PostingNumber: x.PostingNumber, OrderID: id(x.OrderID), OrderNumber: x.OrderNumber, Status: x.Status, Substatus: x.Substatus, InProcessAt: x.InProcessAt, ShipmentDate: x.ShipmentDate, ObservedAt: time.Now().UTC(), ObservedTimeBasis: "detail_received", CancellationPresenceKnown: x.Cancellation != nil, Lines: []domain.PostingLine{}}
			o.ParentPostingNumber = x.ParentPostingNumber
			if x.FactDeliveryDate != nil {
				o.FactDeliveryDate = *x.FactDeliveryDate
			}
			if x.ShipmentDateWithoutDelay != nil {
				o.ShipmentDateWithoutDelay = *x.ShipmentDateWithoutDelay
			}
			if x.DeliveringDate != nil {
				o.DeliveringDate = *x.DeliveringDate
			}
			if x.DeliveryMethod != nil {
				o.WarehouseID = id(x.DeliveryMethod.WarehouseID)
			}
			if x.Cancellation != nil {
				var reasonID *int64
				if x.Cancellation.ReasonID != nil {
					v := int64(*x.Cancellation.ReasonID)
					reasonID = &v
				}
				o.Cancellation = &domain.PostingCancellation{Reason: x.Cancellation.Reason, ReasonID: reasonID, Type: x.Cancellation.Type, Initiator: x.Cancellation.Initiator, CancelledAfterShip: x.Cancellation.After}
			}
			for _, line := range *x.Products {
				if line.Quantity == nil || *line.Quantity < 0 || (line.OfferID == "" && id(line.SKU) == "") {
					return domain.PostingImport{}, errors.New("incomplete posting line")
				}
				o.Lines = append(o.Lines, domain.PostingLine{OfferID: line.OfferID, SKU: id(line.SKU), Quantity: *line.Quantity})
			}
			out.Postings = append(out.Postings, o)
			if len(out.Postings) > maxRows {
				return domain.PostingImport{}, errors.New("posting limit exceeded")
			}
		}
		more, err := nextCursor(page.HasNext, page.Cursor, cursors)
		if err != nil {
			return domain.PostingImport{}, err
		}
		if !more {
			break
		}
		if len(*page.Postings) == 0 {
			return domain.PostingImport{}, errors.New("empty continuation page")
		}
		cursor = page.Cursor
	}
	if err := ctx.Err(); err != nil {
		return domain.PostingImport{}, err
	}
	out.FinishedAt = time.Now().UTC()
	return out, nil
}
