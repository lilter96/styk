package postgres

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func enrichRecoveryPositionBaselines(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, numbers []string) error {
	if len(numbers) == 0 || !result.Config.Enabled || !result.Config.TestEnabled {
		return nil
	}
	var now time.Time
	if err := q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return ordersError("read attestation expiry clock", err)
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT ON(w.posting_number) w.posting_number,w.work,e.evidence,r.id,a.id,a.evidence_id,a.created_at,a.valid_until FROM orders_recovery_resolutions r JOIN orders_work w ON w.id=r.work_id JOIN LATERAL (SELECT * FROM orders_recovery_attestations candidate WHERE candidate.resolution_id=r.id ORDER BY created_at DESC,id DESC LIMIT 1) a ON a.valid_until>clock_timestamp() JOIN orders_recovery_evidence e ON e.id=a.evidence_id WHERE r.tenant_id=$1 AND r.moysklad_account=$2 AND r.ozon_account=$3 AND w.posting_number=ANY($4::text[]) AND e.config_revision=$5 AND e.ozon_credential_revision=$6 AND e.moysklad_credential_revision=$7 AND EXISTS(SELECT 1 FROM saas_merchant_accounts owner JOIN saas_users u ON u.id=owner.user_id WHERE owner.tenant_id=r.tenant_id AND owner.user_id=a.owner_user_id AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL) AND NOT EXISTS(SELECT 1 FROM orders_reconciliations rec JOIN orders_work success ON success.id=rec.work_id WHERE success.tenant_id=r.tenant_id AND success.moysklad_account=r.moysklad_account AND success.ozon_account=r.ozon_account AND success.posting_number=w.posting_number AND rec.created_at>r.created_at) ORDER BY w.posting_number,r.created_at DESC,r.id DESC`, result.Config.Scope.Tenant, result.Config.Scope.MoyskladAccount, result.Config.Scope.OzonAccount, numbers, result.Config.Revision, result.Credentials.Ozon, result.Credentials.Moysklad)
	if err != nil {
		return ordersError("read operator-attested native frontier", err)
	}
	defer rows.Close()
	for rows.Next() {
		var number string
		var rawWork, rawEvidence []byte
		var resolution orders.RecoveryResolution
		var a orders.RecoveryAttestation
		if err = rows.Scan(&number, &rawWork, &rawEvidence, &resolution.ID, &a.ID, &a.EvidenceID, &a.CreatedAt, &a.ValidUntil); err != nil {
			return ordersError("scan recovery position attestation", err)
		}
		var work orders.Work
		var evidence orders.RecoveryEvidence
		if err = ordersDecode(rawWork, &work); err != nil {
			return err
		}
		if err = ordersDecode(rawEvidence, &evidence); err != nil {
			return err
		}
		resolution.Scope, resolution.WorkID, resolution.AcceptedUncertainty, resolution.Attestation = work.Scope, work.ID, true, &a
		baseline, baselineErr := orders.RecoveryPositionBaseline(work, evidence, resolution, now)
		if baselineErr == nil {
			result.PositionBaselines[number] = baseline
		}
	}
	return ordersRowsError("read operator-attested native frontier", rows.Err())
}
