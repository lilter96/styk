package ozon

import (
	"context"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// Discovery's packaged counters are the completeness fence. A bounded posting
// window is never treated as the whole batch unless its identities match them.
func (c *OrdersClient) collectCarriageReady(ctx context.Context, snapshot *orders.CarriageSnapshot, expected int) error {
	if expected > 1000 {
		return &LimitError{Resource: "carriage_members"}
	}
	now := c.now()
	window := orders.OrderWindow{Stream: "ozon-orders-unfulfilled", From: now.Add(-24 * 24 * time.Hour), To: now.Add(7 * 24 * time.Hour), Limit: 100}
	cursor := ""
	seenCursors := map[string]bool{}
	seen := map[string]bool{}
	for pages := 0; pages < 100; pages++ {
		page, err := c.ReadOrderPage(ctx, snapshot.Scope, window, cursor)
		if err != nil {
			return err
		}
		if len(page.Failures) > 0 || (page.Exhausted && !page.Complete) {
			return orders.ErrStale
		}
		if err = c.appendCarriageReady(ctx, snapshot, seen, page.Postings); err != nil {
			return err
		}

		if len(snapshot.Members) > expected {
			return orders.ErrStale
		}
		if page.Exhausted {
			return finishCarriageReady(snapshot, expected)
		}
		if page.NextCursor == "" || seenCursors[page.NextCursor] {
			return orders.ErrSchemaDrift
		}
		cursor = page.NextCursor
		seenCursors[cursor] = true
	}
	return &LimitError{Resource: "carriage_posting_pages"}
}

func (c *OrdersClient) appendCarriageReady(ctx context.Context, snapshot *orders.CarriageSnapshot, seen map[string]bool, postings []orders.Posting) error {
	for _, posting := range postings {
		if orderKnown(posting.DeliveryMethodID) != snapshot.DeliveryMethodID || orderKnown(posting.Status) != "awaiting_deliver" {
			continue
		}
		if seen[posting.PostingNumber] {
			return orders.ErrSchemaDrift
		}
		seen[posting.PostingNumber] = true
		posting, err := c.carriagePostingProfile(ctx, posting, snapshot.DeliveryMethodID, snapshot.WarehouseID)
		if err != nil {
			return err
		}
		member, err := c.carriageReadyMember(posting, snapshot)
		if err != nil {
			return err
		}
		snapshot.Members = append(snapshot.Members, member)
	}
	return nil
}

func finishCarriageReady(snapshot *orders.CarriageSnapshot, expected int) error {
	if len(snapshot.Members) != expected {
		return orders.ErrStale
	}
	slices.SortFunc(snapshot.Members, func(a, b orders.CarriageMember) int { return stringsCompare(a.PostingNumber, b.PostingNumber) })
	return nil
}

func (c *OrdersClient) carriageReadyMember(posting orders.Posting, snapshot *orders.CarriageSnapshot) (orders.CarriageMember, error) {
	member := orders.CarriageMember{PostingNumber: posting.PostingNumber, Scheme: orderKnown(posting.Scheme), WarehouseID: orderKnown(posting.WarehouseID), DeliveryMethodID: orderKnown(posting.DeliveryMethodID), Status: orderKnown(posting.Status), Lines: posting.Lines, Capability: posting.DeliveryProfile}
	member.Blockers = carriagePostingBlockers(posting, snapshot.DeliveryMethodID, snapshot.WarehouseID, c.now())
	member.Ready = len(member.Blockers) == 0
	var err error
	member.SourceFingerprint, err = orders.PostingFingerprint(posting)
	return member, err
}
