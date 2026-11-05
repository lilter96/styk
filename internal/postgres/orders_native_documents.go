package postgres

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func insertNativeDocumentIntent(ctx context.Context, tx pgx.Tx, work orders.Work, now time.Time) error {
	movement := work.Command.NativeMovement
	if movement == nil {
		return nil
	}
	if err := validateNativeConsumptionIntent(ctx, tx, work, now); err != nil {
		return err
	}
	if _, err := readOrderActionSecret(ctx, tx, work.Scope, work.Command.PostingNumber, work.Command.Action, movement.Event.ProtectedInput); err != nil {
		return err
	}
	var basis any
	if movement.DemandBasis != nil {
		basis = movement.DemandBasis.Correlation.DocumentID
	}
	_, err := tx.Exec(ctx, `INSERT INTO orders_native_document_intents(work_id,tenant_id,moysklad_account,ozon_account,kind,posting_number,event_digest,sync_id,marker,customer_order_id,basis_document_id,applicable,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, movement.Correlation.Kind, work.Command.PostingNumber, movement.Event.Digest, movement.Correlation.SyncID, movement.Correlation.Marker, work.Command.Correlation.CustomerOrderID, basis, *movement.Applicable.Value, now)
	if err != nil {
		return ordersError("reserve exact native movement event", err)
	}
	for _, line := range movement.Lines {
		if err = insertNativeDocumentTarget(ctx, tx, work, line); err != nil {
			return err
		}
	}
	return nil
}

func insertNativeDocumentTarget(ctx context.Context, tx pgx.Tx, work orders.Work, line orders.NativeMovementLine) error {
	mappingID, err := strconv.ParseInt(line.Mapping.ID, 10, 64)
	if err != nil || mappingID < 1 {
		return orders.ErrInvalid
	}
	var basisPosition any
	if line.BasisPositionID != "" {
		basisPosition = line.BasisPositionID
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_native_document_targets(work_id,tenant_id,moysklad_account,ozon_account,store_id,mapping_id,mapping_version,item_kind,item_id,quantity,native_order_position_id,basis_position_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, work.Command.NativeMovement.StoreID, mappingID, line.Mapping.Version, line.Mapping.MoyskladKind, line.Mapping.MoyskladID, string(line.Quantity), line.OrderPositionID, basisPosition)
	return ordersRowsError("bind native movement to owned physical pool", err)
}

func saveNativeDocumentResult(ctx context.Context, tx pgx.Tx, work orders.Work, completion orders.Completion, now time.Time) error {
	if !work.Command.Action.IsNativeMovement() || completion.State != orders.StateReconciled {
		return nil
	}
	observation := completion.ObservedNativeDocument
	if observation == nil {
		return orders.ErrInvalid
	}
	_, err := tx.Exec(ctx, `INSERT INTO orders_native_document_results(work_id,tenant_id,moysklad_account,ozon_account,kind,native_document_id,posting_number,customer_order_id,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, observation.Correlation.Kind, observation.Correlation.DocumentID, work.Command.PostingNumber, work.Command.Correlation.CustomerOrderID, now)
	return ordersRowsError("record verified native document at database clock", err)
}

func (s *Store) OrderNativeDocument(ctx context.Context, scope orders.Scope, workID string) (orders.NativeDocumentBinding, error) {
	if scope.Validate() != nil || workID == "" {
		return orders.NativeDocumentBinding{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readNativeDocumentBinding(ctx, s.Pool, scope, workID)
}

func readNativeDocumentBinding(ctx context.Context, q ordersQuerier, scope orders.Scope, workID string) (orders.NativeDocumentBinding, error) {
	var result orders.NativeDocumentBinding
	var raw, parent []byte
	err := q.QueryRow(ctx, `SELECT w.id,w.posting_number,i.event_digest,r.observed_native_document,r.observed_target FROM orders_work w JOIN orders_native_document_intents i ON i.work_id=w.id AND i.tenant_id=w.tenant_id AND i.moysklad_account=w.moysklad_account AND i.ozon_account=w.ozon_account JOIN orders_reconciliations r ON r.work_id=w.id AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account JOIN orders_native_document_results n ON n.work_id=w.id AND n.tenant_id=w.tenant_id AND n.moysklad_account=w.moysklad_account AND n.ozon_account=w.ozon_account WHERE w.id=$4 AND w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.state='RECONCILED' AND w.action IN ('demand_create','salesreturn_create')`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, workID).Scan(&result.WorkID, &result.PostingNumber, &result.EventDigest, &raw, &parent)
	if err != nil {
		return result, ordersError("read owned native document binding", err)
	}
	if err = ordersDecode(raw, &result.Observation); err != nil {
		return result, err
	}
	if err = ordersDecode(parent, &result.Parent); err != nil {
		return result, err
	}
	if !result.Parent.Complete || result.Parent.Scope != scope || result.Parent.Correlation.CustomerOrderID == "" {
		return result, orders.ErrStale
	}
	if result.Observation.Scope != scope || result.Observation.Validate() != nil {
		return result, orders.ErrStale
	}
	return result, nil
}

func enrichNativePreviewContext(ctx context.Context, q ordersQuerier, preview orders.Preview, current *orders.ReviewContext) error {
	current.NativeDocuments = make(map[string]orders.NativeDocumentBinding)
	for _, decision := range preview.Decisions {
		for _, command := range decision.Commands {
			if command.NativeMovement == nil || command.NativeMovement.DemandWorkID == "" {
				continue
			}
			binding, err := readNativeDocumentBinding(ctx, q, preview.Scope, command.NativeMovement.DemandWorkID)
			if err != nil {
				return err
			}
			current.NativeDocuments[binding.WorkID] = binding
		}
	}
	return nil
}
