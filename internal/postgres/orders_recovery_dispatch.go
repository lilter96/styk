package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// Only unsent recovery decisions need a live attestation. Already dispatched
// operations remain eligible for read-only reconciliation after it expires.
func recoveryDispatchCurrent(ctx context.Context, tx pgx.Tx, work orders.Work) (bool, error) {
	if work.DispatchCount > 0 {
		return true, nil
	}
	var valid bool
	err := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM orders_recovery_links WHERE work_id=$1) OR EXISTS(SELECT 1 FROM orders_recovery_links l JOIN orders_recovery_attestations a ON a.id=l.attestation_id JOIN orders_recovery_resolutions r ON r.id=l.resolution_id JOIN orders_recovery_evidence e ON e.id=a.evidence_id JOIN saas_merchant_accounts owner ON owner.tenant_id=a.tenant_id AND owner.user_id=a.owner_user_id JOIN saas_users u ON u.id=owner.user_id WHERE l.work_id=$1 AND a.valid_until>clock_timestamp() AND e.config_revision=$2 AND e.ozon_credential_revision=$3 AND e.moysklad_credential_revision=$4 AND r.native_kind='customerorder' AND r.native_id=$5 AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL)`, work.ID, work.ConfigRevision, work.Credentials.Ozon, work.Credentials.Moysklad, work.Command.Correlation.CustomerOrderID).Scan(&valid)
	if err != nil {
		return false, ordersError("validate recovery decision authorization", err)
	}
	return valid, nil
}
