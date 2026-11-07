package ozon

import (
	"context"
	"reflect"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

const carriageGetPath = "/v1/carriage/get"
const carriageMembersPath = "/v2/posting/fbs/act/get-postings"

func carriageReadPath(path string) bool {
	return path == carriageGetPath || path == carriageMembersPath || path == carriageDiscoveryPath
}

func (c *OrdersClient) ReadCarriage(ctx context.Context, scope orders.Scope, id string) (orders.CarriageSnapshot, error) {
	if scope != c.scope || !orders.CarriageIDValid(id) {
		return orders.CarriageSnapshot{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	header, err := c.readCarriageHeader(ctx, id)
	if err != nil {
		return orders.CarriageSnapshot{}, err
	}
	data, err := c.orderPost(ctx, carriageMembersPath, map[string]any{"id": jsonCarriageID(id)})
	if err != nil {
		return orders.CarriageSnapshot{}, err
	}
	root := newOrderFields(data)
	if ordercontract.CheckJSON(data) != nil || root.raw["result"] == nil || string(root.raw["result"]) == "null" {
		return orders.CarriageSnapshot{}, orders.ErrSchemaDrift
	}
	rows := root.array("result")
	if len(rows) > 1000 || *root.err != nil {
		return orders.CarriageSnapshot{}, orders.ErrSchemaDrift
	}
	snapshot := orders.CarriageSnapshot{Scope: scope, DeliveryMethodID: header.DeliveryMethodID, WarehouseID: header.WarehouseID, CarriageID: id, Header: &header, ContractVersion: "ozon-carriage-known-id-2026-11-07", Members: make([]orders.CarriageMember, 0, len(rows)), CandidateIDs: []string{}, Blockers: []orders.Validation{}}
	snapshot.Blockers = append(snapshot.Blockers, orders.Validation{Code: "CARRIAGE_APPROVAL_CONTRACT_UNVERIFIED"})
	if err = c.readCarriageMembers(ctx, &snapshot, rows); err != nil {
		return orders.CarriageSnapshot{}, err
	}
	if err = c.carriageMembershipUnchanged(ctx, id, rows); err != nil {
		return orders.CarriageSnapshot{}, err
	}
	after, err := c.readCarriageHeader(ctx, id)
	if err != nil || !reflect.DeepEqual(header, after) {
		return orders.CarriageSnapshot{}, orders.ErrStale
	}
	slices.SortFunc(snapshot.Members, func(a, b orders.CarriageMember) int { return stringsCompare(a.PostingNumber, b.PostingNumber) })
	snapshot.Complete, snapshot.ObservedAt = true, c.now()
	snapshot.Fingerprint, err = orders.CarriageSnapshotFingerprint(snapshot)
	return snapshot, err
}

func (c *OrdersClient) carriageMembershipUnchanged(ctx context.Context, id string, before []orderFields) error {
	data, err := c.orderPost(ctx, carriageMembersPath, map[string]any{"id": jsonCarriageID(id)})
	if err != nil {
		return err
	}
	root := newOrderFields(data)
	if ordercontract.CheckJSON(data) != nil || root.raw["result"] == nil || string(root.raw["result"]) == "null" {
		return orders.ErrSchemaDrift
	}
	project := func(rows []orderFields) ([]orders.CarriageMember, error) {
		members := make([]orders.CarriageMember, 0, len(rows))
		for _, row := range rows {
			member, e := decodeCarriageMember(row)
			if e != nil {
				return nil, e
			}
			members = append(members, member)
		}
		slices.SortFunc(members, func(a, b orders.CarriageMember) int { return stringsCompare(a.PostingNumber, b.PostingNumber) })
		return members, nil
	}
	first, err := project(before)
	if err != nil {
		return err
	}
	second, err := project(root.array("result"))
	if err != nil || *root.err != nil || !reflect.DeepEqual(first, second) {
		return orders.ErrStale
	}
	return nil
}

func (c *OrdersClient) readCarriageHeader(ctx context.Context, id string) (orders.CarriageHeader, error) {
	data, err := c.orderPost(ctx, carriageGetPath, map[string]any{"carriage_id": jsonCarriageID(id)})
	if err != nil {
		return orders.CarriageHeader{}, err
	}
	header, err := decodeCarriageHeader(data)
	if err != nil {
		return header, err
	}
	if header.CarriageID != id || header.CompanyID != c.scope.OzonAccount {
		return header, orders.ErrStale
	}
	return header, nil
}

func (c *OrdersClient) readCarriageMembers(ctx context.Context, snapshot *orders.CarriageSnapshot, rows []orderFields) error {
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		member, err := decodeCarriageMember(row)
		if err != nil || seen[member.PostingNumber] {
			return orders.ErrSchemaDrift
		}
		seen[member.PostingNumber] = true
		posting, err := c.ReadOrderPosting(ctx, c.scope, member.PostingNumber)
		if err != nil {
			return err
		}
		if !carriageLinesEqual(member.Lines, posting.Lines) {
			return orders.ErrStale
		}
		member.Status = orderKnown(posting.Status)
		member.Scheme, member.WarehouseID, member.DeliveryMethodID = orderKnown(posting.Scheme), orderKnown(posting.WarehouseID), orderKnown(posting.DeliveryMethodID)
		member.Capability = posting.DeliveryProfile
		member.Blockers = carriagePostingBlockers(posting, snapshot.DeliveryMethodID, snapshot.WarehouseID, c.now())
		member.Ready = len(member.Blockers) == 0
		member.SourceFingerprint, err = orders.PostingFingerprint(posting)
		if err != nil {
			return err
		}
		snapshot.Members = append(snapshot.Members, member)
	}
	return nil
}

func carriagePostingBlockers(posting orders.Posting, method, warehouse string, now time.Time) []orders.Validation {
	issues := make([]orders.Validation, 0)
	add := func(code string) { issues = append(issues, orders.Validation{Code: code}) }
	if orderKnown(posting.Status) != "awaiting_deliver" {
		add("CARRIAGE_POSTING_NOT_READY")
	}
	if orderKnown(posting.Scheme) != "FBS" || orderKnown(posting.DeliveryMethodID) != method || orderKnown(posting.WarehouseID) != warehouse {
		add("CARRIAGE_PROFILE_MISMATCH")
	}
	capability := posting.DeliveryProfile
	if capability == nil || capability.Validate() != nil || capability.Scheme != "FBS" || capability.WarehouseID != warehouse || capability.DeliveryMethodID != method || !capability.Express.IsKnown() || *capability.Express.Value || !slices.Contains(capability.VerifiedDimensions, "scheme") || !slices.Contains(capability.VerifiedDimensions, "warehouse") || !slices.Contains(capability.VerifiedDimensions, "express") || !now.Before(capability.ValidUntil) {
		add("CARRIAGE_PROFILE_UNVERIFIED")
	}
	if len(posting.Drift) != 0 {
		add("CARRIAGE_POSTING_SCHEMA_REVIEW_REQUIRED")
	}
	return issues
}
