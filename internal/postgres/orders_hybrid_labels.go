package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) OrderLabelProof(ctx context.Context, scope orders.Scope, posting, workID string) (orders.LabelProof, error) {
	session, ok := identity.SessionFromContext(ctx)
	if !ok || scope.Validate() != nil || posting == "" || len(posting) > 256 || workID == "" || len(workID) > 256 {
		return orders.LabelProof{}, orders.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.LabelProof{}, ordersError("begin label prerequisite read", err)
	}
	defer rollbackMerchantMutation(tx)
	if _, err = ordersActor(ctx, tx, scope.Tenant, session.AuditID); err != nil {
		return orders.LabelProof{}, orders.ErrNotFound
	}
	proof, err := readOrderLabelProof(ctx, tx, scope, posting, workID)
	if err != nil {
		return orders.LabelProof{}, err
	}
	return proof, ordersRowsError("finish label prerequisite read", tx.Commit(ctx))
}

func readOrderLabelProof(ctx context.Context, q ordersQuerier, scope orders.Scope, posting, workID string) (orders.LabelProof, error) {
	proof := orders.LabelProof{Scope: scope, WorkID: workID, PostingNumber: posting}
	var raw []byte
	err := q.QueryRow(ctx, `SELECT w.work,a.task_id,a.expires_at FROM orders_work w JOIN orders_label_artifacts a ON a.work_id=w.id AND a.tenant_id=w.tenant_id AND a.moysklad_account=w.moysklad_account AND a.ozon_account=w.ozon_account WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=$4 AND w.id=$5 AND w.action='posting_label_create' AND w.state='RECONCILED' AND a.expires_at>clock_timestamp()`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, posting, workID).Scan(&raw, &proof.TaskID, &proof.ExpiresAt)
	if err != nil {
		return proof, ordersError("read exact reconciled label prerequisite", err)
	}
	var work orders.Work
	if err = ordersDecode(raw, &work); err != nil {
		return proof, err
	}
	if work.Scope != scope || work.ID != workID || work.Command.PostingNumber != posting || work.Command.Action != orders.ActionPostingLabelCreate || work.Receipt.TaskID != proof.TaskID || work.Receipt.LabelTask == nil || !orders.LabelTaskComplete(work.Command, work.Receipt, *work.Receipt.LabelTask) {
		return proof, orders.ErrStale
	}
	proof.SourceRevision = work.Command.ExpectedSourceRevision
	proof.CommercialOrderID = work.Command.CommercialOrderID
	proof.WarehouseID, proof.DeliveryMethodID = work.Command.Warehouse.OzonWarehouseID, work.Command.Warehouse.Capability.DeliveryMethodID
	return proof, nil
}

func validateOrderHybridLabel(ctx context.Context, q ordersQuerier, posting orders.Posting, command orders.Command) error {
	if !orders.HybridLabelRequired(posting, command.Action) {
		return nil
	}
	if command.PostingAction == nil || !command.PostingAction.Parameters.LabelPrintedConfirmed || command.PostingAction.Parameters.LabelWorkID == "" {
		return orders.ErrBlocked
	}
	proof, err := readOrderLabelProof(ctx, q, command.Scope, command.PostingNumber, command.PostingAction.Parameters.LabelWorkID)
	if errors.Is(err, orders.ErrNotFound) {
		return orders.ErrStale
	}
	if err != nil {
		return err
	}
	var now time.Time
	err = q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	if err != nil {
		return ordersError("check label prerequisite expiry", err)
	}
	return proof.ValidateFor(posting, command.PostingAction.Parameters.LabelWorkID, now)
}

func validateOrderPreviewHybridLabels(ctx context.Context, q ordersQuerier, preview orders.Preview, current orders.ReviewContext) error {
	postings := make(map[string]orders.Posting, len(current.Postings))
	for _, posting := range current.Postings {
		postings[posting.PostingNumber] = posting
	}
	for _, decision := range preview.Decisions {
		for _, command := range decision.Commands {
			posting, ok := postings[command.PostingNumber]
			if !ok {
				return orders.ErrStale
			}
			if err := validateOrderHybridLabel(ctx, q, posting, command); err != nil {
				return err
			}
		}
	}
	return nil
}
