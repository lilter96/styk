package postgres

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) OrderReviewContext(ctx context.Context, scope orders.Scope) (orders.ReviewContext, error) {
	if scope.Validate() != nil {
		return orders.ReviewContext{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return orders.ReviewContext{}, ordersError("begin order review context", err)
	}
	defer rollbackMerchantMutation(tx)
	result, err := readOrderReviewContext(ctx, tx, scope)
	if err != nil {
		return result, err
	}
	return result, ordersRowsError("commit order review context", tx.Commit(ctx))
}

func readOrderReviewContext(ctx context.Context, q ordersQuerier, scope orders.Scope) (orders.ReviewContext, error) {
	var result orders.ReviewContext
	var err error
	result.Config, err = readOrderConfig(ctx, q, scope)
	if err != nil {
		return result, err
	}
	result.Credentials, err = ordersCredentials(ctx, q, scope)
	if err != nil {
		return result, err
	}
	result.Postings, err = readOrderPostings(ctx, q, scope, "", "", 10001)
	if err != nil {
		return result, err
	}
	if len(result.Postings) > 10000 {
		return result, orders.ErrBlocked
	}
	result.ProductMappings, err = readOrderProductMappings(ctx, q, scope)
	if err != nil {
		return result, err
	}
	result.WarehouseMappings, err = readOrderWarehouseMappings(ctx, q, scope)
	if err != nil {
		return result, err
	}
	result.Correlations, err = readOrderCorrelations(ctx, q, scope)
	if err != nil {
		return result, err
	}
	result.Metadata, err = readOrderMetadata(ctx, q, scope)
	return result, err
}

func readOrderProductMappings(ctx context.Context, q ordersQuerier, scope orders.Scope) ([]orders.ProductMappingRef, error) {
	rows, err := q.Query(ctx, `SELECT m.id,m.version,m.ozon_product_id,m.ozon_offer_id,COALESCE((SELECT p.sku FROM ozon_product_observations p WHERE p.import_id=h.ozon_import_id AND p.product_id=m.ozon_product_id),''),m.target_kind,m.target_id,m.confirmed_at FROM product_mappings m JOIN product_mapping_history h ON h.mapping_id=m.id AND h.version=m.version AND h.tenant_id=m.tenant_id AND h.moysklad_account=m.moysklad_account AND h.ozon_account=m.ozon_account AND h.target_kind=m.target_kind AND h.target_id=m.target_id AND h.ozon_product_id=m.ozon_product_id AND h.ozon_offer_id=m.ozon_offer_id JOIN stock_connections mc ON mc.tenant_id=m.tenant_id AND mc.source='moysklad' AND mc.account_id=m.moysklad_account JOIN stock_connections oc ON oc.tenant_id=m.tenant_id AND oc.source='ozon' AND oc.account_id=m.ozon_account JOIN source_imports mi ON mi.id=h.moysklad_import_id AND mi.source='moysklad' AND mi.account_id=m.moysklad_account JOIN source_imports oi ON oi.id=h.ozon_import_id AND oi.source='ozon' AND oi.account_id=m.ozon_account WHERE m.tenant_id=$1 AND m.moysklad_account=$2 AND m.ozon_account=$3 AND (mc.include_import_history OR (mi.started_at>=mc.registered_at AND m.confirmed_at>=mc.registered_at)) AND (oc.include_import_history OR (oi.started_at>=oc.registered_at AND m.confirmed_at>=oc.registered_at)) ORDER BY m.id`, ordersScopeArgs(scope)...)
	if err != nil {
		return nil, ordersError("select order product mappings", err)
	}
	defer rows.Close()
	mappings := make([]orders.ProductMappingRef, 0, 100)
	for rows.Next() {
		mapping := orders.ProductMappingRef{Scope: scope, Confirmed: true}
		var id int64
		if err = rows.Scan(&id, &mapping.Version, &mapping.OzonProductID, &mapping.OfferID, &mapping.SKU, &mapping.MoyskladKind, &mapping.MoyskladID, &mapping.ConfirmedAt); err != nil {
			return nil, ordersError("scan order product mapping", err)
		}
		mapping.ID = strconv.FormatInt(id, 10)
		mappings = append(mappings, mapping)
	}
	return mappings, ordersRowsError("read order product mappings", rows.Err())
}

func readOrderWarehouseMappings(ctx context.Context, q ordersQuerier, scope orders.Scope) ([]orders.WarehouseMappingRef, error) {
	rows, err := q.Query(ctx, `SELECT m.id,m.version,m.ozon_warehouse_id,m.moysklad_warehouse_id,m.confirmed_at FROM warehouse_mappings m JOIN warehouse_mapping_history h ON h.mapping_id=m.id AND h.version=m.version AND h.tenant_id=m.tenant_id AND h.moysklad_account=m.moysklad_account AND h.ozon_account=m.ozon_account AND h.moysklad_warehouse_id=m.moysklad_warehouse_id AND h.ozon_warehouse_id=m.ozon_warehouse_id JOIN stock_connections mc ON mc.tenant_id=m.tenant_id AND mc.source='moysklad' AND mc.account_id=m.moysklad_account JOIN stock_connections oc ON oc.tenant_id=m.tenant_id AND oc.source='ozon' AND oc.account_id=m.ozon_account JOIN source_imports mi ON mi.id=h.moysklad_import_id AND mi.source='moysklad' AND mi.account_id=m.moysklad_account JOIN source_imports oi ON oi.id=h.ozon_import_id AND oi.source='ozon' AND oi.account_id=m.ozon_account WHERE m.tenant_id=$1 AND m.moysklad_account=$2 AND m.ozon_account=$3 AND m.status='active' AND (mc.include_import_history OR (mi.started_at>=mc.registered_at AND m.confirmed_at>=mc.registered_at)) AND (oc.include_import_history OR (oi.started_at>=oc.registered_at AND m.confirmed_at>=oc.registered_at)) ORDER BY m.id`, ordersScopeArgs(scope)...)
	if err != nil {
		return nil, ordersError("select order warehouse mappings", err)
	}
	defer rows.Close()
	mappings := make([]orders.WarehouseMappingRef, 0, 10)
	for rows.Next() {
		mapping := orders.WarehouseMappingRef{Scope: scope, Confirmed: true}
		var id int64
		if err = rows.Scan(&id, &mapping.Version, &mapping.OzonWarehouseID, &mapping.MoyskladStoreID, &mapping.ConfirmedAt); err != nil {
			return nil, ordersError("scan order warehouse mapping", err)
		}
		mapping.ID = strconv.FormatInt(id, 10)
		mappings = append(mappings, mapping)
	}
	return mappings, ordersRowsError("read order warehouse mappings", rows.Err())
}

func readOrderCorrelations(ctx context.Context, q ordersQuerier, scope orders.Scope) ([]orders.Correlation, error) {
	rows, err := q.Query(ctx, `SELECT posting_number,sync_id,marker,COALESCE(customer_order_id,'') FROM orders_projection_bindings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 ORDER BY posting_number`, ordersScopeArgs(scope)...)
	if err != nil {
		return nil, ordersError("select order projection bindings", err)
	}
	defer rows.Close()
	result := make([]orders.Correlation, 0, 100)
	for rows.Next() {
		correlation := orders.Correlation{Scope: scope}
		if err = rows.Scan(&correlation.PostingNumber, &correlation.SyncID, &correlation.Marker, &correlation.CustomerOrderID); err != nil {
			return nil, ordersError("scan order projection binding", err)
		}
		result = append(result, correlation)
	}
	return result, ordersRowsError("read order projection bindings", rows.Err())
}

func (s *Store) OrderPreview(ctx context.Context, scope orders.Scope, id string) (orders.Preview, error) {
	if scope.Validate() != nil || id == "" {
		return orders.Preview{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readOrderPreview(ctx, s.Pool, scope, id)
}

func readOrderPreview(ctx context.Context, q ordersQuerier, scope orders.Scope, id string) (orders.Preview, error) {
	var preview orders.Preview
	var raw []byte
	var actor string
	args := append(ordersScopeArgs(scope), id)
	err := q.QueryRow(ctx, `SELECT preview,actor FROM orders_previews WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4`, args...).Scan(&raw, &actor)
	if err != nil {
		return preview, ordersError("read order preview", err)
	}
	if err = ordersDecode(raw, &preview); err != nil {
		return preview, err
	}
	preview.Actor = actor
	return preview, nil
}

func (s *Store) SaveOrderPreview(ctx context.Context, preview orders.Preview) (orders.Preview, error) {
	if preview.Scope.Validate() != nil || preview.ID == "" || len(preview.Decisions) > 100 || !preview.ExpiresAt.After(preview.CreatedAt) {
		return orders.Preview{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.Preview{}, ordersError("begin order preview", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockOrderReviewScope(ctx, tx, preview.Scope); err != nil {
		return orders.Preview{}, err
	}
	owner, err := orderPreviewOwner(ctx, tx, preview)
	if err != nil {
		return orders.Preview{}, err
	}
	if err = validateAndInsertOrderPreview(ctx, tx, preview, owner); err != nil {
		return orders.Preview{}, err
	}
	return preview, ordersRowsError("commit order preview", tx.Commit(ctx))
}

func lockOrderReviewScope(ctx context.Context, tx pgx.Tx, scope orders.Scope) error {
	if err := ordersLock(ctx, tx, scope); err != nil {
		return err
	}
	if err := ordersCredentialLocks(ctx, tx, scope); err != nil {
		return err
	}
	return ordersMappingLocks(ctx, tx, scope)
}

func validateAndInsertOrderPreview(ctx context.Context, tx pgx.Tx, preview orders.Preview, owner string) error {
	current, err := readOrderReviewContext(ctx, tx, preview.Scope)
	if err != nil {
		return err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	if err = validateOrderPreview(preview, current, now); err != nil {
		return err
	}
	raw, err := ordersJSON(preview)
	if err != nil {
		return err
	}
	hash, err := ordersHash(preview)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_previews(id,tenant_id,moysklad_account,ozon_account,config_revision,preview,fingerprint,actor,automatic,owner_user_id,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, preview.ID, preview.Scope.Tenant, preview.Scope.MoyskladAccount, preview.Scope.OzonAccount, preview.ConfigRevision, raw, hash, preview.Actor, preview.Automatic, owner, preview.CreatedAt, preview.ExpiresAt)
	return ordersRowsError("save immutable order preview", err)
}

func orderPreviewOwner(ctx context.Context, tx pgx.Tx, preview orders.Preview) (string, error) {
	if !preview.Automatic {
		return ordersActor(ctx, tx, preview.Scope.Tenant, preview.Actor)
	}
	config, err := readOrderConfig(ctx, tx, preview.Scope)
	if err != nil {
		return "", err
	}
	return ordersGrantOwner(ctx, tx, config, preview.Actor)
}

func validateOrderPreview(preview orders.Preview, current orders.ReviewContext, now time.Time) error {
	if !current.Config.Enabled || !current.Config.TestEnabled {
		return orders.ErrBlocked
	}
	if current.Config.Revision != preview.ConfigRevision || current.Credentials != preview.Credentials || !preview.ExpiresAt.After(now) || preview.CreatedAt.After(now.Add(time.Minute)) || preview.ExpiresAt.Sub(preview.CreatedAt) > time.Duration(current.Config.PreviewTTLSeconds)*time.Second {
		return orders.ErrStale
	}
	postings := make(map[string]orders.Posting, len(current.Postings))
	for _, posting := range current.Postings {
		postings[posting.PostingNumber] = posting
	}
	for _, decision := range preview.Decisions {
		posting, ok := postings[decision.Posting.PostingNumber]
		if !ok || decision.Posting.Scope != preview.Scope || decision.Posting.Evidence.Revision != posting.Evidence.Revision {
			return orders.ErrStale
		}
		if err := validateOrderDecision(decision, current, preview.Automatic, now); err != nil {
			return err
		}
	}
	return nil
}
