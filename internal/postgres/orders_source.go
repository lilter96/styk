package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func saveOrderSourcePermission(ctx context.Context, tx pgx.Tx, scope orders.Scope, owner, actor string, enabled bool) error {
	args := append(ordersScopeArgs(scope), enabled, owner, actor)
	_, err := tx.Exec(ctx, `INSERT INTO orders_source_permissions(tenant_id,moysklad_account,ozon_account,enabled,owner_user_id,actor) VALUES($1,$2,$3,$4,$5,$6)
 ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET enabled=EXCLUDED.enabled,owner_user_id=EXCLUDED.owner_user_id,actor=EXCLUDED.actor,consented_at=clock_timestamp()`, args...)
	if err != nil {
		return ordersError("save order source permission", err)
	}
	if enabled {
		return nil
	}
	_, err = tx.Exec(ctx, `UPDATE orders_source_refreshes SET state='CANCELLED',code='SOURCE_DISABLED',finished_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND state IN('QUEUED','READING')`, ordersScopeArgs(scope)...)
	return ordersRowsError("cancel disabled order source refreshes", err)
}

func validateSourceReadOwner(ctx context.Context, tx pgx.Tx, scope orders.Scope) error {
	var owner string
	err := tx.QueryRow(ctx, `SELECT owner_user_id FROM orders_source_permissions WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND enabled FOR SHARE`, ordersScopeArgs(scope)...).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return orders.ErrBlocked
	}
	if err != nil {
		return ordersError("read order source owner", err)
	}
	return ordersOwnerCurrent(ctx, tx, scope.Tenant, owner)
}

// prepareOrderSource locks the same scope and credentials as configuration and
// ingestion. It never changes an existing target policy or automation grant.
func prepareOrderSource(ctx context.Context, tx pgx.Tx, scope orders.Scope, actor string) (string, error) {
	if scope.Validate() != nil {
		return "", orders.ErrInvalid
	}
	owner, err := ordersActor(ctx, tx, scope.Tenant, actor)
	if err != nil {
		return "", err
	}
	if err = ordersLock(ctx, tx, scope); err != nil {
		return "", err
	}
	if err = ordersCredentialLocks(ctx, tx, scope); err != nil {
		return "", err
	}
	return owner, nil
}

func queueOrderSource(ctx context.Context, tx pgx.Tx, scope orders.Scope, id, owner, actor string) error {
	revisions, err := ordersCredentials(ctx, tx, scope)
	if err != nil {
		return err
	}
	if revisions.Ozon < 1 || revisions.Moysklad < 1 {
		return orders.ErrBlocked
	}

	if err = ensureSourceOrderConfig(ctx, tx, scope, id, owner, actor); err != nil {
		return err
	}
	if err = saveOrderSourcePermission(ctx, tx, scope, owner, actor, true); err != nil {
		return err
	}
	args := append(ordersScopeArgs(scope), id)
	if _, err = tx.Exec(ctx, `INSERT INTO orders_source_refreshes(tenant_id,moysklad_account,ozon_account,import_id) VALUES($1,$2,$3,$4)`, args...); err != nil {
		return ordersError("queue order source refresh", err)
	}
	_, err = tx.Exec(ctx, `UPDATE orders_configs SET next_run_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, ordersScopeArgs(scope)...)
	return ordersRowsError("schedule order source refresh", err)
}

func ensureSourceOrderConfig(ctx context.Context, tx pgx.Tx, scope orders.Scope, id, owner, actor string) error {
	config, err := readOrderConfig(ctx, tx, scope)
	if err != nil {
		return err
	}
	if config.Revision == 0 {
		req := orders.SaveConfigRequest{Config: config, Actor: actor, IdempotencyKey: "source:" + id}
		hash, hashErr := ordersHash(req)
		if hashErr != nil {
			return hashErr
		}
		if _, err = insertOrderConfig(ctx, tx, req, owner, hash); err != nil {
			return err
		}
	}
	return nil
}
