package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) SaveOrderRecoveryEvidence(ctx context.Context, evidence orders.RecoveryEvidence) (orders.RecoveryView, error) {
	if evidence.Scope.Validate() != nil || !ordersRequestKey(evidence.ID) || !ordersRequestKey(evidence.WorkID) || evidence.Actor == "" {
		return orders.RecoveryView{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.RecoveryView{}, ordersError("begin recovery evidence", err)
	}
	defer rollbackMerchantMutation(tx)
	owner, err := ordersActor(ctx, tx, evidence.Scope.Tenant, evidence.Actor)
	if err != nil {
		return orders.RecoveryView{}, orders.ErrNotFound
	}
	if err = lockOrderReviewScope(ctx, tx, evidence.Scope); err != nil {
		return orders.RecoveryView{}, err
	}
	current, err := readOrderRecoveryContext(ctx, tx, evidence.Scope, evidence.WorkID)
	if err != nil {
		return orders.RecoveryView{}, err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return orders.RecoveryView{}, err
	}
	if err = validateRecoveryEvidence(evidence, current, now); err != nil {
		return orders.RecoveryView{}, err
	}
	evidence.CreatedAt, evidence.ExpiresAt = now, now.Add(orders.RecoveryEvidenceTTL)
	if err = insertRecoveryEvidence(ctx, tx, evidence, owner); err != nil {
		return orders.RecoveryView{}, err
	}
	current, err = readOrderRecoveryContext(ctx, tx, evidence.Scope, evidence.WorkID)
	if err != nil {
		return orders.RecoveryView{}, err
	}
	return current.View, ordersRowsError("commit recovery evidence", tx.Commit(ctx))
}

func validateRecoveryEvidence(e orders.RecoveryEvidence, current orders.RecoveryContext, now time.Time) error {
	w := current.View.Work
	if w.State != orders.StateUnknown || e.CommandFingerprint != w.CommandFingerprint || e.ConfigRevision != current.Config.Revision || e.Credentials != current.Credentials || e.StoredSourceRevision != current.Posting.Evidence.Revision {
		return orders.ErrStale
	}
	if e.Source != nil && (e.Source.PostingNumber != w.Command.PostingNumber || e.Source.CommercialOrderID != w.Command.CommercialOrderID || e.Source.Fingerprint == "" || !recoveryTimeFresh(e.Source.ObservedAt, now)) {
		return orders.ErrStale
	}
	if e.Target == nil {
		return nil
	}
	t := e.Target
	if t.Kind != "customerorder" || t.Correlation.Scope != w.Scope || t.Correlation.PostingNumber != w.Command.PostingNumber || t.Correlation.SyncID != w.Command.Correlation.SyncID || t.Correlation.Marker != w.Command.Correlation.Marker || !t.Complete || t.Revision == "" || len(t.Positions) > 1000 || !recoveryTimeFresh(t.ObservedAt, now) {
		return orders.ErrStale
	}
	if w.Command.Correlation.CustomerOrderID != "" && t.Correlation.CustomerOrderID != w.Command.Correlation.CustomerOrderID {
		return orders.ErrStale
	}
	return nil
}

func recoveryTimeFresh(at, now time.Time) bool {
	return !at.IsZero() && !at.After(now.Add(time.Minute)) && now.Sub(at) <= orders.RecoveryEvidenceTTL
}

func insertRecoveryEvidence(ctx context.Context, tx pgx.Tx, e orders.RecoveryEvidence, owner string) error {
	raw, err := ordersJSON(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_recovery_evidence(id,work_id,tenant_id,moysklad_account,ozon_account,config_revision,ozon_credential_revision,moysklad_credential_revision,actor,owner_user_id,evidence,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, e.ID, e.WorkID, e.Scope.Tenant, e.Scope.MoyskladAccount, e.Scope.OzonAccount, e.ConfigRevision, e.Credentials.Ozon, e.Credentials.Moysklad, e.Actor, owner, raw, e.CreatedAt, e.ExpiresAt)
	return ordersRowsError("persist sanitized recovery evidence", err)
}
