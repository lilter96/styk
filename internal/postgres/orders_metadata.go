package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

var _ orders.MetadataCreator = (*Store)(nil)

func (s *Store) OrderMetadata(ctx context.Context, scope orders.Scope) (orders.MetadataRegistry, error) {
	if scope.Validate() != nil {
		return orders.MetadataRegistry{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readOrderMetadata(ctx, s.Pool, scope)
}

func readOrderMetadata(ctx context.Context, q ordersQuerier, scope orders.Scope) (orders.MetadataRegistry, error) {
	registry := orders.MetadataRegistry{Scope: scope, Attributes: []orders.AttributeDefinition{}}
	var raw []byte
	err := q.QueryRow(ctx, `SELECT registry FROM orders_metadata_revisions WHERE tenant_id=$1 AND moysklad_account=$2 ORDER BY revision DESC LIMIT 1`, scope.Tenant, scope.MoyskladAccount).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return registry, nil
	}
	if err != nil {
		return registry, ordersError("read order metadata registry", err)
	}
	if err = ordersDecode(raw, &registry); err != nil {
		return registry, err
	}
	registry.Scope = scope
	return registry, nil
}

func ordersMetadataLock(ctx context.Context, tx pgx.Tx, scope orders.Scope) error {
	key := "orders-metadata:" + scope.Tenant + ":" + scope.MoyskladAccount
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key)
	return ordersRowsError("lock order metadata registry", err)
}

func (s *Store) SaveOrderMetadata(ctx context.Context, registry orders.MetadataRegistry, expected int64) (orders.MetadataRegistry, error) {
	if registry.Validate() != nil || expected < 0 {
		return orders.MetadataRegistry{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.MetadataRegistry{}, ordersError("begin order metadata registry", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = ordersMetadataLock(ctx, tx, registry.Scope); err != nil {
		return orders.MetadataRegistry{}, err
	}
	result, err := saveOrderMetadataLocked(ctx, tx, registry, expected)
	if err != nil {
		return orders.MetadataRegistry{}, err
	}
	return result, ordersRowsError("commit order metadata registry", tx.Commit(ctx))
}

func saveOrderMetadataLocked(ctx context.Context, tx pgx.Tx, registry orders.MetadataRegistry, expected int64) (orders.MetadataRegistry, error) {
	previous, err := readOrderMetadata(ctx, tx, registry.Scope)
	if err != nil {
		return registry, err
	}
	if previous.Revision != expected {
		return registry, orders.ErrStale
	}
	if registry.CreationIntentID != "" {
		if err = validateOrderMetadataCreation(ctx, tx, registry); err != nil {
			return registry, err
		}
	}
	for _, attribute := range registry.Attributes {
		if err = saveOrderAttribute(ctx, tx, registry, attribute); err != nil {
			return registry, err
		}
	}
	registry.Revision = expected + 1
	raw, err := ordersJSON(registry)
	if err != nil {
		return registry, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_metadata_revisions(tenant_id,moysklad_account,revision,registry,observed_at) VALUES($1,$2,$3,$4,$5)`, registry.Scope.Tenant, registry.Scope.MoyskladAccount, registry.Revision, raw, registry.ObservedAt)
	return registry, ordersRowsError("save observed order attribute UUIDs", err)
}

func saveOrderAttribute(ctx context.Context, tx pgx.Tx, registry orders.MetadataRegistry, attribute orders.AttributeDefinition) error {
	_, err := tx.Exec(ctx, `INSERT INTO orders_metadata_attributes(tenant_id,moysklad_account,unique_code,attribute_id,attribute_type,created_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, registry.Scope.Tenant, registry.Scope.MoyskladAccount, attribute.UniqueCode, attribute.AttributeID, attribute.Type, registry.ObservedAt)
	if err != nil {
		return ordersError("save order attribute identity", err)
	}
	var id, kind string
	err = tx.QueryRow(ctx, `SELECT attribute_id,attribute_type FROM orders_metadata_attributes WHERE tenant_id=$1 AND moysklad_account=$2 AND unique_code=$3`, registry.Scope.Tenant, registry.Scope.MoyskladAccount, attribute.UniqueCode).Scan(&id, &kind)
	if err != nil {
		return ordersError("read order attribute identity", err)
	}
	if id != attribute.AttributeID || kind != attribute.Type {
		return orders.ErrConflict
	}
	return nil
}

func validateOrderMetadataCreation(ctx context.Context, tx pgx.Tx, registry orders.MetadataRegistry) error {
	var code, id, kind string
	err := tx.QueryRow(ctx, `SELECT unique_code,attribute_id,attribute_type FROM orders_metadata_creation_intents WHERE id=$1 AND tenant_id=$2 AND moysklad_account=$3 AND state='RECONCILED'`, registry.CreationIntentID, registry.Scope.Tenant, registry.Scope.MoyskladAccount).Scan(&code, &id, &kind)
	if err != nil {
		return ordersError("check order metadata creation readback", err)
	}
	for _, attribute := range registry.Attributes {
		if attribute.UniqueCode == code && attribute.AttributeID == id && attribute.Type == kind {
			return nil
		}
	}
	return orders.ErrStale
}

func (s *Store) BeginOrderMetadataCreation(ctx context.Context, req orders.MetadataCreationRequest, _ time.Time) (orders.MetadataCreationIntent, error) {
	if req.Scope.Validate() != nil || !ordersRequestKey(req.UniqueCode) || !ordersRequestKey(req.IdempotencyKey) || req.Type == "" {
		return orders.MetadataCreationIntent{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.MetadataCreationIntent{}, ordersError("begin order metadata creation", err)
	}
	defer rollbackMerchantMutation(tx)
	owner, err := ordersActor(ctx, tx, req.Scope.Tenant, req.Actor)
	if err != nil {
		return orders.MetadataCreationIntent{}, err
	}
	if err = ordersMetadataLock(ctx, tx, req.Scope); err != nil {
		return orders.MetadataCreationIntent{}, err
	}
	intent, err := beginOrderMetadataLocked(ctx, tx, req, owner)
	if err != nil {
		return orders.MetadataCreationIntent{}, err
	}
	return intent, ordersRowsError("commit order metadata creation intent", tx.Commit(ctx))
}

func beginOrderMetadataLocked(ctx context.Context, tx pgx.Tx, req orders.MetadataCreationRequest, owner string) (orders.MetadataCreationIntent, error) {
	hash, err := ordersHash(req)
	if err != nil {
		return orders.MetadataCreationIntent{}, err
	}
	var raw []byte
	var previous string
	err = tx.QueryRow(ctx, `SELECT intent,request_hash FROM orders_metadata_creation_intents WHERE tenant_id=$1 AND moysklad_account=$2 AND request_key=$3`, req.Scope.Tenant, req.Scope.MoyskladAccount, req.IdempotencyKey).Scan(&raw, &previous)
	if err == nil {
		var intent orders.MetadataCreationIntent
		if previous != hash {
			return intent, orders.ErrConflict
		}
		return intent, ordersDecode(raw, &intent)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return orders.MetadataCreationIntent{}, ordersError("read order metadata intent replay", err)
	}
	return insertOrderMetadataIntent(ctx, tx, req, owner, hash)
}

func insertOrderMetadataIntent(ctx context.Context, tx pgx.Tx, req orders.MetadataCreationRequest, owner, hash string) (orders.MetadataCreationIntent, error) {
	id, err := ordersID()
	if err != nil {
		return orders.MetadataCreationIntent{}, err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return orders.MetadataCreationIntent{}, err
	}
	intent := orders.MetadataCreationIntent{ID: id, Scope: req.Scope, UniqueCode: req.UniqueCode, Type: req.Type, Marker: "styk-order-attribute-" + id, State: orders.StateDispatching, IdempotencyKey: req.IdempotencyKey, CreatedAt: now, DispatchPermitted: true}
	raw, err := ordersJSON(intent)
	if err != nil {
		return intent, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_metadata_creation_intents(id,tenant_id,moysklad_account,unique_code,attribute_type,fingerprint,intent,state,actor,owner_user_id,request_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,'DISPATCHING',$8,$9,$10,$11,$12)`, id, req.Scope.Tenant, req.Scope.MoyskladAccount, req.UniqueCode, req.Type, hash, raw, req.Actor, owner, req.IdempotencyKey, hash, now)
	return intent, ordersRowsError("journal one-shot order metadata intent", err)
}

func (s *Store) CompleteOrderMetadataCreation(ctx context.Context, completion orders.MetadataCreationCompletion) (orders.MetadataRegistry, error) {
	if completion.Scope.Validate() != nil || completion.IntentID == "" {
		return orders.MetadataRegistry{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.MetadataRegistry{}, ordersError("begin metadata creation result", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = ordersMetadataLock(ctx, tx, completion.Scope); err != nil {
		return orders.MetadataRegistry{}, err
	}
	registry, err := completeOrderMetadataLocked(ctx, tx, completion)
	if err != nil {
		return orders.MetadataRegistry{}, err
	}
	return registry, ordersRowsError("commit metadata creation result", tx.Commit(ctx))
}

func completeOrderMetadataLocked(ctx context.Context, tx pgx.Tx, completion orders.MetadataCreationCompletion) (orders.MetadataRegistry, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT intent FROM orders_metadata_creation_intents WHERE id=$1 AND tenant_id=$2 AND moysklad_account=$3 FOR UPDATE`, completion.IntentID, completion.Scope.Tenant, completion.Scope.MoyskladAccount).Scan(&raw)
	if err != nil {
		return orders.MetadataRegistry{}, ordersError("lock metadata creation result", err)
	}
	var intent orders.MetadataCreationIntent
	if err = ordersDecode(raw, &intent); err != nil {
		return orders.MetadataRegistry{}, err
	}
	if intent.State != completion.ExpectedState || !validOrderMetadataTransition(intent.State, completion.State) {
		return orders.MetadataRegistry{}, orders.ErrStale
	}
	intent.State, intent.AttributeID = completion.State, completion.AttributeID
	if err = writeOrderMetadataResult(ctx, tx, intent, completion); err != nil {
		return orders.MetadataRegistry{}, err
	}
	registry, err := readOrderMetadata(ctx, tx, completion.Scope)
	if err != nil || completion.State != orders.StateReconciled {
		return registry, err
	}
	registry.Attributes = append(registry.Attributes, orders.AttributeDefinition{UniqueCode: intent.UniqueCode, AttributeID: intent.AttributeID, Type: intent.Type})
	registry.CreationIntentID, registry.ObservedAt = intent.ID, completion.ObservedAt
	if err = registry.Validate(); err != nil {
		return registry, err
	}
	return saveOrderMetadataLocked(ctx, tx, registry, registry.Revision)
}

func validOrderMetadataTransition(from, to orders.State) bool {
	return (from == orders.StateDispatching || from == orders.StateUnknown) && (to == orders.StateReconciled || to == orders.StateUnknown || to == orders.StateRejected)
}

func writeOrderMetadataResult(ctx context.Context, tx pgx.Tx, intent orders.MetadataCreationIntent, completion orders.MetadataCreationCompletion) error {
	raw, err := ordersJSON(intent)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE orders_metadata_creation_intents SET state=$2,attribute_id=NULLIF($3,''),intent=$4 WHERE id=$1`, intent.ID, intent.State, intent.AttributeID, raw); err != nil {
		return ordersError("save order metadata creation result", err)
	}
	result, err := ordersJSON(completion)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_metadata_creation_results(intent_id,result,created_at) VALUES($1,$2,$3)`, intent.ID, result, completion.ObservedAt)
	return ordersRowsError(fmt.Sprintf("journal order metadata %s result", ordersSafeCode(string(intent.State))), err)
}
