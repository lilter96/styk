package postgres

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) ApproveOrderPreview(ctx context.Context, approval orders.Approval, _ time.Time) (orders.Operation, error) {
	if approval.Scope.Validate() != nil || approval.PreviewID == "" || !ordersRequestKey(approval.IdempotencyKey) {
		return orders.Operation{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.Operation{}, ordersError("begin order approval", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockOrderReviewScope(ctx, tx, approval.Scope); err != nil {
		return orders.Operation{}, err
	}
	owner, err := orderPreviewOwner(ctx, tx, orders.Preview{Scope: approval.Scope, Actor: approval.Actor, Automatic: approval.Automatic})
	if err != nil {
		return orders.Operation{}, err
	}
	operation, err := approveOrderLocked(ctx, tx, approval, owner)
	if err != nil {
		return orders.Operation{}, err
	}
	return operation, ordersRowsError("commit order approval", tx.Commit(ctx))
}

func approveOrderLocked(ctx context.Context, tx pgx.Tx, approval orders.Approval, owner string) (orders.Operation, error) {
	hash, err := ordersHash(struct {
		Approval orders.Approval
		Actor    string
		Auto     bool
	}{approval, approval.Actor, approval.Automatic})
	if err != nil {
		return orders.Operation{}, err
	}
	replay, err := replayOrderApproval(ctx, tx, approval, hash)
	if err == nil {
		return replay, nil
	}
	if !errors.Is(err, orders.ErrNotFound) {
		return orders.Operation{}, err
	}
	preview, err := readOrderPreview(ctx, tx, approval.Scope, approval.PreviewID)
	if err != nil {
		return orders.Operation{}, err
	}
	current, err := readOrderReviewContext(ctx, tx, approval.Scope)
	if err != nil {
		return orders.Operation{}, err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return orders.Operation{}, err
	}
	decisions, err := approvedOrderDecisions(approval, preview, current, now)
	if err != nil {
		return orders.Operation{}, err
	}
	return insertOrderOperation(ctx, tx, approval, preview, decisions, owner, hash, now)
}

func replayOrderApproval(ctx context.Context, tx pgx.Tx, approval orders.Approval, hash string) (orders.Operation, error) {
	var id, previous string
	args := append(ordersScopeArgs(approval.Scope), approval.IdempotencyKey)
	err := tx.QueryRow(ctx, `SELECT id,request_hash FROM orders_operations WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND request_key=$4`, args...).Scan(&id, &previous)
	if err != nil {
		return orders.Operation{}, ordersError("read order approval replay", err)
	}
	if hash != previous {
		return orders.Operation{}, orders.ErrConflict
	}
	return readOrderOperation(ctx, tx, approval.Scope, id)
}

func approvedOrderDecisions(approval orders.Approval, preview orders.Preview, current orders.ReviewContext, now time.Time) ([]orders.Decision, error) {
	if preview.Actor != approval.Actor || preview.Automatic != approval.Automatic {
		return nil, orders.ErrBlocked
	}
	if err := validateOrderPreview(preview, current, now); err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(approval.PostingNumbers))
	for _, number := range approval.PostingNumbers {
		if selected[number] {
			return nil, orders.ErrInvalid
		}
		selected[number] = true
	}
	decisions := make([]orders.Decision, 0, len(preview.Decisions))
	for _, decision := range preview.Decisions {
		if len(approval.PostingNumbers) > 0 && !selected[decision.Posting.PostingNumber] {
			continue
		}
		delete(selected, decision.Posting.PostingNumber)
		if !decision.Ready || len(decision.Validation) > 0 || ordersQuarantined(decision.Posting) || len(decision.Commands) != 1 {
			return nil, orders.ErrBlocked
		}
		decisions = append(decisions, decision)
	}
	if len(selected) > 0 || len(decisions) == 0 {
		return nil, orders.ErrBlocked
	}
	return decisions, nil
}

func insertOrderOperation(ctx context.Context, tx pgx.Tx, approval orders.Approval, preview orders.Preview, decisions []orders.Decision, owner, hash string, now time.Time) (orders.Operation, error) {
	id, err := ordersID()
	if err != nil {
		return orders.Operation{}, err
	}
	operation := orders.Operation{ID: id, Scope: approval.Scope, PreviewID: preview.ID, State: orders.StateQueued, CreatedAt: now, Targets: make([]orders.Work, 0, len(decisions))}
	_, err = tx.Exec(ctx, `INSERT INTO orders_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,request_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, approval.Scope.Tenant, approval.Scope.MoyskladAccount, approval.Scope.OzonAccount, preview.ConfigRevision, preview.ID, approval.Actor, owner, approval.Automatic, approval.IdempotencyKey, hash, now)
	if err != nil {
		return operation, ordersError("save order approval audit", err)
	}
	for _, decision := range decisions {
		work, insertErr := insertOrderWork(ctx, tx, operation, preview, decision.Commands[0], approval.Automatic, now)
		if insertErr != nil {
			return operation, insertErr
		}
		operation.Targets = append(operation.Targets, work)
	}
	return operation, nil
}

func insertOrderWork(ctx context.Context, tx pgx.Tx, operation orders.Operation, preview orders.Preview, command orders.Command, automatic bool, now time.Time) (orders.Work, error) {
	id, err := ordersID()
	if err != nil {
		return orders.Work{}, err
	}
	hash, err := orders.CommandFingerprint(command)
	if err != nil {
		return orders.Work{}, err
	}
	work := orders.Work{ID: id, OperationID: operation.ID, Scope: operation.Scope, PreviewID: preview.ID, ConfigRevision: preview.ConfigRevision, Credentials: preview.Credentials, Command: command, CommandFingerprint: hash, State: orders.StateQueued, Automatic: automatic, NextRunAt: now}
	raw, err := ordersJSON(work)
	if err != nil {
		return work, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)`, id, operation.ID, operation.Scope.Tenant, operation.Scope.MoyskladAccount, operation.Scope.OzonAccount, preview.ConfigRevision, command.PostingNumber, command.ID, hash, command.Action, work.State, raw, now)
	if err != nil {
		return work, ordersError("fence order posting target", err)
	}
	if err = insertOrderWorkMappings(ctx, tx, work); err != nil {
		return work, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_tasks(operation_key,task_type,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts,available_at) VALUES($1,'orders_sync',$2,$3,$4,$5,$6,1,$7)`, "orders-sync:"+id, operation.ID, id, operation.Scope.Tenant, operation.Scope.MoyskladAccount, operation.Scope.OzonAccount, now)
	return work, ordersRowsError("enqueue reviewed order command", err)
}

func insertOrderWorkMappings(ctx context.Context, tx pgx.Tx, work orders.Work) error {
	warehouse := work.Command.Warehouse
	warehouseID, err := strconv.ParseInt(warehouse.ID, 10, 64)
	if err != nil || warehouseID < 1 {
		return orders.ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_work_warehouses(work_id,tenant_id,moysklad_account,ozon_account,mapping_id,mapping_version,store_id,warehouse_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, warehouseID, warehouse.Version, warehouse.MoyskladStoreID, warehouse.OzonWarehouseID)
	if err != nil {
		return ordersError("freeze order warehouse mapping", err)
	}
	if work.Command.CustomerOrder == nil {
		return nil
	}
	for _, position := range work.Command.CustomerOrder.Positions {
		if err = insertOrderWorkProductMapping(ctx, tx, work, position.Mapping); err != nil {
			return err
		}
	}
	return nil
}

func insertOrderWorkProductMapping(ctx context.Context, tx pgx.Tx, work orders.Work, mapping orders.ProductMappingRef) error {
	id, err := strconv.ParseInt(mapping.ID, 10, 64)
	if err != nil || id < 1 {
		return orders.ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_work_mappings(work_id,tenant_id,moysklad_account,ozon_account,mapping_id,mapping_version,target_kind,target_id,product_id,offer_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, id, mapping.Version, mapping.MoyskladKind, mapping.MoyskladID, mapping.OzonProductID, mapping.OfferID)
	return ordersRowsError("freeze order line product mapping", err)
}
