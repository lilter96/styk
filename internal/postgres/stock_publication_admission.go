package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
)

// This is a readiness hint before a durable dispatch claim, not a reservation
// or an external-ordering guarantee. The shared transport still admits and
// authorizes every actual request. All waits use the provider's DB-clock state.
// SHA-256 byte keys match providercall.normalize, including its NUL separator.
const stockPublicationProviderReadySQL = `
 NOT EXISTS (
  SELECT 1 FROM provider_call_accounts a
  WHERE a.provider='ozon' AND a.account_id=p.ozon_account
   AND (a.cooldown_blocked OR GREATEST(a.next_send_at,a.cooldown_until)>clock_timestamp()
    OR (SELECT count(*) FROM provider_call_permits permit
        WHERE permit.provider=a.provider AND permit.account_id=a.account_id
         AND permit.state IN ('reserved','sending') AND permit.lease_until>clock_timestamp())>=a.max_inflight)
 )
 AND NOT EXISTS (
  SELECT 1 FROM provider_call_methods method
  WHERE method.provider='ozon' AND method.account_id=p.ozon_account
   AND method.method_key=encode(sha256(convert_to('POST','UTF8')||decode('00','hex')||convert_to('v2/products/stocks','UTF8')),'hex')
   AND method.cooldown_until>clock_timestamp()
 )
 AND NOT EXISTS (
  SELECT 1 FROM stock_publication_targets queued
  JOIN provider_call_targets target ON target.provider='ozon' AND target.account_id=p.ozon_account
   AND target.target_key=encode(sha256(convert_to(queued.product_id||':'||queued.warehouse_id,'UTF8')),'hex')
  WHERE queued.operation_id=p.id AND queued.state='QUEUED' AND target.next_send_at>clock_timestamp()
 )`

func stockPublicationProviderReady(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, id string) (bool, error) {
	var ready bool
	err := tx.QueryRow(ctx, `SELECT `+stockPublicationProviderReadySQL+` FROM stock_publications p
 WHERE p.id=$1 AND p.tenant_id=$2 AND p.moysklad_account=$3 AND p.ozon_account=$4`, id, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&ready)
	if err != nil {
		return false, fmt.Errorf("check stock provider admission readiness: %w", err)
	}
	return ready, nil
}
