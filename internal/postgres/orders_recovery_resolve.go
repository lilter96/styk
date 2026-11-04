package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) ResolveOrderRecovery(ctx context.Context, request orders.RecoveryResolveRequest) (orders.RecoveryView, error) {
	if request.Validate() != nil || !ordersRequestKey(request.AttestationID) || len(request.ReasonCiphertext) < 29 || len(request.ReasonCiphertext) > 16384 || len(request.ReasonDigest) != 64 {
		return orders.RecoveryView{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.RecoveryView{}, ordersError("begin order supersession", err)
	}
	defer rollbackMerchantMutation(tx)
	owner, err := ordersActor(ctx, tx, request.Scope.Tenant, request.Actor)
	if err != nil {
		return orders.RecoveryView{}, orders.ErrNotFound
	}
	if err = lockOrderReviewScope(ctx, tx, request.Scope); err != nil {
		return orders.RecoveryView{}, err
	}
	if err = lockRecoveryTarget(ctx, tx, request); err != nil {
		return orders.RecoveryView{}, err
	}
	hash, err := orderRecoveryHash(request, owner)
	if err != nil {
		return orders.RecoveryView{}, err
	}
	replay, err := replayOrderRecovery(ctx, tx, request, hash)
	if err != nil {
		return orders.RecoveryView{}, err
	}
	if !replay {
		if err = resolveOrderRecoveryLocked(ctx, tx, request, owner, hash); err != nil {
			return orders.RecoveryView{}, err
		}
	}
	current, err := readOrderRecoveryContext(ctx, tx, request.Scope, request.WorkID)
	if err != nil {
		return orders.RecoveryView{}, err
	}
	return current.View, ordersRowsError("commit audited order supersession", tx.Commit(ctx))
}

func lockRecoveryTarget(ctx context.Context, tx pgx.Tx, request orders.RecoveryResolveRequest) error {
	var id string
	args := append(ordersScopeArgs(request.Scope), request.WorkID)
	err := tx.QueryRow(ctx, `SELECT w.id FROM orders_work w JOIN outbox_tasks q ON q.orders_work_id=w.id AND q.task_type='orders_sync' WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.id=$4 FOR UPDATE OF w,q`, args...).Scan(&id)
	if err != nil {
		return ordersError("lock exact recovery target", err)
	}
	// Admissions take the same account rows. No connection is held during HTTP.
	_, err = tx.Exec(ctx, `SELECT 1 FROM provider_call_accounts WHERE (provider='moysklad' AND account_id=$1) OR (provider='ozon' AND account_id=$2) ORDER BY provider,account_id FOR UPDATE`, request.Scope.MoyskladAccount, request.Scope.OzonAccount)
	return ordersRowsError("lock recovery provider admissions", err)
}

func orderRecoveryHash(request orders.RecoveryResolveRequest, owner string) (string, error) {
	return ordersHash(struct {
		Scope                                   orders.Scope
		WorkID, EvidenceID, Owner, ReasonDigest string
		Accepted, Stopped                       bool
	}{request.Scope, request.WorkID, request.EvidenceID, owner, request.ReasonDigest, request.Acknowledge, request.WorkerStopped})
}

func replayOrderRecovery(ctx context.Context, tx pgx.Tx, request orders.RecoveryResolveRequest, hash string) (bool, error) {
	var previous string
	args := append(ordersScopeArgs(request.Scope), request.IdempotencyKey)
	err := tx.QueryRow(ctx, `SELECT request_hash FROM orders_recovery_attestations WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND request_key=$4`, args...).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ordersError("read recovery request replay", err)
	}
	if previous != hash {
		return false, orders.ErrConflict
	}
	return true, nil
}

func resolveOrderRecoveryLocked(ctx context.Context, tx pgx.Tx, request orders.RecoveryResolveRequest, owner, hash string) error {
	current, err := readOrderRecoveryContext(ctx, tx, request.Scope, request.WorkID)
	if err != nil {
		return err
	}
	if !current.View.Eligible {
		return orders.ErrBlocked
	}
	if current.View.Evidence.ID != request.EvidenceID {
		return orders.ErrStale
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	resolutionID := request.AttestationID
	if current.View.Resolution == nil {
		if err = insertOrderResolution(ctx, tx, request, current.View.Work, owner, hash, now); err != nil {
			return err
		}
	} else {
		resolutionID = current.View.Resolution.ID
	}
	if err = insertRecoveryAttestation(ctx, tx, request, resolutionID, owner, hash, now, current.View.Evidence.ExpiresAt); err != nil {
		return err
	}
	if err = insertRecoveryReason(ctx, tx, request, now, current.Config); err != nil {
		return err
	}
	if current.View.Resolution != nil {
		return nil
	}
	return stopRecoveredReadback(ctx, tx, request.WorkID)
}

func stopRecoveredReadback(ctx context.Context, tx pgx.Tx, workID string) error {
	tag, err := tx.Exec(ctx, `UPDATE outbox_tasks SET state='failed',lease_until=NULL,claim_token=NULL,last_error='OPERATOR_SUPERSEDED_UNKNOWN',updated_at=clock_timestamp() WHERE orders_work_id=$1 AND task_type='orders_sync' AND state='pending' AND lease_until IS NULL`, workID)
	if err != nil {
		return ordersError("stop superseded readback task", err)
	}
	if tag.RowsAffected() != 1 {
		return orders.ErrStale
	}
	tag, err = tx.Exec(ctx, `DELETE FROM orders_target_fences WHERE work_id=$1`, workID)
	if err != nil {
		return ordersError("release audited exact-native order fence", err)
	}
	if tag.RowsAffected() != 1 {
		return orders.ErrStale
	}
	return nil
}

func insertOrderResolution(ctx context.Context, tx pgx.Tx, request orders.RecoveryResolveRequest, work orders.Work, owner, hash string, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO orders_recovery_resolutions(id,work_id,tenant_id,moysklad_account,ozon_account,evidence_id,native_kind,native_id,actor,owner_user_id,accepted_uncertainty,worker_stopped,reason_digest,request_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,'customerorder',$7,$8,$9,true,true,$10,$11,$12,$13)`, request.AttestationID, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, request.EvidenceID, work.Command.Correlation.CustomerOrderID, request.Actor, owner, request.ReasonDigest, request.IdempotencyKey, hash, now)
	return ordersRowsError("append immutable accepted-uncertainty resolution", err)
}

func insertRecoveryAttestation(ctx context.Context, tx pgx.Tx, request orders.RecoveryResolveRequest, resolution, owner, hash string, now, expires time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO orders_recovery_attestations(id,resolution_id,work_id,tenant_id,moysklad_account,ozon_account,evidence_id,actor,owner_user_id,accepted_uncertainty,worker_stopped,reason_digest,request_key,request_hash,created_at,valid_until) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,true,true,$10,$11,$12,$13,$14)`, request.AttestationID, resolution, request.WorkID, request.Scope.Tenant, request.Scope.MoyskladAccount, request.Scope.OzonAccount, request.EvidenceID, request.Actor, owner, request.ReasonDigest, request.IdempotencyKey, hash, now, expires)
	return ordersRowsError("append expiring recovery attestation", err)
}

func insertRecoveryReason(ctx context.Context, tx pgx.Tx, request orders.RecoveryResolveRequest, now time.Time, config orders.Config) error {
	_, err := tx.Exec(ctx, `INSERT INTO orders_recovery_reason_secrets(attestation_id,ciphertext,created_at,expires_at) VALUES($1,$2,$3,$4)`, request.AttestationID, request.ReasonCiphertext, now, now.Add(config.LabelRetention()))
	return ordersRowsError("persist protected expiring recovery reason", err)
}
