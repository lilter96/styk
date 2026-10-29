package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// GuardOrderMetadataCreation rechecks the authenticated owner and exact native
// credential revision after provider admission, then journals one dispatch.
// Its transaction always commits before HTTP; existing append-only results
// provide the durable one-use fence without another queue or migration.
func (s *Store) GuardOrderMetadataCreation(ctx context.Context, intent orders.MetadataCreationIntent, actor string, revision int64) (bool, error) {
	if !validOrderMetadataGuard(intent, actor, revision) {
		return false, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, ordersError("begin metadata dispatch authorization", err)
	}
	defer rollbackMerchantMutation(tx)
	owner, err := ordersActor(ctx, tx, intent.Scope.Tenant, actor)
	if err != nil {
		return false, err
	}
	if err = ordersMetadataLock(ctx, tx, intent.Scope); err != nil {
		return false, err
	}
	if err = validateOrderMetadataGuardLocked(ctx, tx, intent, actor, owner, revision); err != nil {
		return false, err
	}
	if err = journalOrderMetadataDispatch(ctx, tx, intent, actor, revision); err != nil {
		return false, err
	}
	return true, ordersRowsError("commit metadata dispatch authorization", tx.Commit(ctx))
}

func validOrderMetadataGuard(intent orders.MetadataCreationIntent, actor string, revision int64) bool {
	if intent.Scope.Validate() != nil || intent.ID == "" || actor == "" || revision < 1 || intent.State != orders.StateDispatching || !intent.DispatchPermitted {
		return false
	}
	if intent.Marker != "styk-order-attribute-"+intent.ID || (intent.Type != "string" && intent.Type != "text") {
		return false
	}
	switch intent.UniqueCode {
	case "styk-order-id", "styk-order-status", "styk-order-substatus":
		return true
	}
	return false
}

func validateOrderMetadataGuardLocked(ctx context.Context, tx pgx.Tx, intent orders.MetadataCreationIntent, actor, owner string, revision int64) error {
	var raw []byte
	var storedActor, storedOwner string
	err := tx.QueryRow(ctx, `SELECT intent,actor,owner_user_id FROM orders_metadata_creation_intents WHERE id=$1 AND tenant_id=$2 AND moysklad_account=$3 AND state='DISPATCHING' FOR UPDATE`, intent.ID, intent.Scope.Tenant, intent.Scope.MoyskladAccount).Scan(&raw, &storedActor, &storedOwner)
	if err != nil {
		return ordersError("lock exact metadata intent", err)
	}
	var stored orders.MetadataCreationIntent
	if err = ordersDecode(raw, &stored); err != nil {
		return err
	}
	if !sameOrderMetadataIntent(intent, stored) || actor != storedActor || owner != storedOwner {
		return orders.ErrStale
	}
	if err = ordersOwnerCurrent(ctx, tx, intent.Scope.Tenant, owner); err != nil {
		return err
	}
	return validateOrderMetadataCredential(ctx, tx, intent.Scope, revision)
}

func sameOrderMetadataIntent(expected, stored orders.MetadataCreationIntent) bool {
	if !expected.CreatedAt.Equal(stored.CreatedAt) {
		return false
	}
	expected.CreatedAt, stored.CreatedAt = time.Time{}, time.Time{}
	expected.DispatchPermitted, stored.DispatchPermitted = false, false
	return expected == stored
}

func validateOrderMetadataCredential(ctx context.Context, tx pgx.Tx, scope orders.Scope, expected int64) error {
	var revision int64
	err := tx.QueryRow(ctx, `SELECT c.revision FROM connection_credentials c JOIN stock_connections s ON s.tenant_id=c.tenant_id AND s.source=c.provider AND s.account_id=c.account_id WHERE c.tenant_id=$1 AND c.provider='moysklad' AND c.account_id=$2 AND c.enabled FOR SHARE OF c,s`, scope.Tenant, scope.MoyskladAccount).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return orders.ErrStale
	}
	if err != nil {
		return ordersError("verify metadata credential revision", err)
	}
	if revision != expected {
		return orders.ErrStale
	}
	return nil
}

func journalOrderMetadataDispatch(ctx context.Context, tx pgx.Tx, intent orders.MetadataCreationIntent, actor string, revision int64) error {
	var used bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_metadata_creation_results WHERE intent_id=$1 AND result->>'phase'='DISPATCH_AUTHORIZED')`, intent.ID).Scan(&used)
	if err != nil {
		return ordersError("check metadata dispatch fence", err)
	}
	if used {
		return orders.ErrStale
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	event := struct {
		Phase              string    `json:"phase"`
		IntentID           string    `json:"intent_id"`
		Actor              string    `json:"actor"`
		CredentialRevision int64     `json:"credential_revision"`
		ObservedAt         time.Time `json:"observed_at"`
	}{"DISPATCH_AUTHORIZED", intent.ID, actor, revision, now}
	raw, err := ordersJSON(event)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_metadata_creation_results(intent_id,result,created_at) VALUES($1,$2,$3)`, intent.ID, raw, now)
	return ordersRowsError("journal one metadata dispatch permission", err)
}
