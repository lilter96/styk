package postgres

import (
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

type nativeConsumptionRow struct {
	work, document, parent, targets []byte
	id, fingerprint                 string
	state                           orders.State
	created                         time.Time
	dispatchCount                   int
	writeIntent, writeValid         bool
	intentValid                     *bool
	documentID                      *string
	confirmed                       *time.Time
	event                           orders.NativeConsumptionEvent
}

type nativeConsumptionTarget struct {
	BasisPositionID string         `json:"basis_position_id"`
	PositionID      string         `json:"position_id"`
	Quantity        orders.Decimal `json:"quantity"`
	MappingID       string         `json:"mapping_id"`
	MappingVersion  int64          `json:"mapping_version"`
	StoreID         string         `json:"store_id"`
	ItemKind        string         `json:"item_kind"`
	ItemID          string         `json:"item_id"`
}

func (r *nativeConsumptionRow) scan(row pgx.Row) error {
	return row.Scan(&r.work, &r.id, &r.state, &r.dispatchCount, &r.fingerprint, &r.created, &r.writeIntent, &r.writeValid, &r.intentValid, &r.documentID, &r.confirmed, &r.document, &r.parent, &r.targets)
}

func (r nativeConsumptionRow) bytes() int {
	return len(r.work) + len(r.document) + len(r.parent) + len(r.targets)
}

func (r *nativeConsumptionRow) valid() bool {
	if !r.writeValid || r.intentValid == nil || !*r.intentValid || ordersDecode(r.work, &r.event.Work) != nil {
		return false
	}
	w := r.event.Work
	if w.ID != r.id || w.State != r.state || w.DispatchCount != r.dispatchCount || w.CommandFingerprint != r.fingerprint || w.Scope != w.Command.Scope {
		return false
	}
	fingerprint, err := orders.CommandFingerprint(w.Command)
	if err != nil || fingerprint != r.fingerprint || !r.targetsMatch(w.Command.NativeMovement) {
		return false
	}
	r.event.WriteIntent = orders.Present(r.writeIntent)
	r.event.CreatedAt = r.created
	return r.observations()
}

func (r *nativeConsumptionRow) observations() bool {
	if len(r.document) > 0 {
		r.event.Document = &orders.NativeDocumentObservation{}
		if ordersDecode(r.document, r.event.Document) != nil || r.documentID == nil || *r.documentID != r.event.Document.Correlation.DocumentID {
			return false
		}
	}
	if len(r.parent) > 0 {
		r.event.Parent = &orders.TargetObservation{}
		if ordersDecode(r.parent, r.event.Parent) != nil {
			return false
		}
	}
	if r.confirmed != nil {
		r.event.ConfirmedAt = *r.confirmed
	}
	return (r.documentID == nil) == (r.event.Document == nil) && (r.confirmed == nil) == (r.event.Document == nil)
}

func (r nativeConsumptionRow) targetsMatch(movement *orders.NativeMovement) bool {
	var targets []nativeConsumptionTarget
	if movement == nil || ordersDecode(r.targets, &targets) != nil || len(targets) != len(movement.Lines) {
		return false
	}
	byID := make(map[string]nativeConsumptionTarget, len(targets))
	for _, target := range targets {
		if _, duplicate := byID[target.PositionID]; duplicate {
			return false
		}
		byID[target.PositionID] = target
	}
	for _, line := range movement.Lines {
		target, found := byID[line.OrderPositionID]
		if !found || !orders.ExactDecimalEqual(target.Quantity, line.Quantity) || target.MappingID != line.Mapping.ID || target.MappingVersion != line.Mapping.Version || target.ItemKind != line.Mapping.MoyskladKind || target.ItemID != line.Mapping.MoyskladID || target.BasisPositionID != line.BasisPositionID || target.StoreID != movement.StoreID {
			return false
		}
	}
	return true
}
