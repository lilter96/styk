package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

func saveOrderExemplarSecret(ctx context.Context, tx pgx.Tx, work orders.Work, c orders.Completion, now time.Time) error {
	if c.ExemplarSecret == nil {
		return nil
	}
	s := *c.ExemplarSecret
	if !work.Command.Action.IsExemplar() || s.WorkID != work.ID || s.Scope != work.Scope || s.PostingNumber != work.Command.PostingNumber || s.CredentialRevision != work.Credentials.Ozon || s.TargetFingerprint != work.Command.PostingAction.PackingTargetFingerprint || s.CreatedAt.After(now.Add(time.Minute)) || !s.ExpiresAt.After(now) {
		return orders.ErrStale
	}
	config, err := readOrderConfig(ctx, tx, work.Scope)
	if err != nil {
		return err
	}
	if config.Revision != work.ConfigRevision || s.ExpiresAt.After(s.CreatedAt.Add(config.LabelRetention())) || s.Public.WorkID != s.WorkID || s.Public.TargetFingerprint != s.TargetFingerprint {
		return orders.ErrStale
	}
	public, err := ordersJSON(s.Public)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_exemplar_secrets(work_id,tenant_id,moysklad_account,ozon_account,posting_number,target_fingerprint,credential_revision,digest,public_snapshot,ciphertext,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, s.WorkID, s.Scope.Tenant, s.Scope.MoyskladAccount, s.Scope.OzonAccount, s.PostingNumber, s.TargetFingerprint, s.CredentialRevision, s.Digest, public, s.Ciphertext, s.CreatedAt, s.ExpiresAt)
	return ordersRowsError("persist protected exemplar evidence", err)
}

func (s *Store) OrderExemplarSecret(ctx context.Context, scope orders.Scope, workID string) (orders.ExemplarSecret, error) {
	if scope.Validate() != nil || workID == "" || len(workID) > 256 {
		return orders.ExemplarSecret{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readOrderExemplarSecret(ctx, s.Pool, scope, workID)
}

func readOrderExemplarSecret(ctx context.Context, q ordersQuerier, scope orders.Scope, workID string) (orders.ExemplarSecret, error) {
	s := orders.ExemplarSecret{Scope: scope, WorkID: workID}
	var raw []byte
	err := q.QueryRow(ctx, `SELECT e.posting_number,e.target_fingerprint,e.credential_revision,e.digest,e.public_snapshot,e.ciphertext,e.created_at,e.expires_at,(w.state='RECONCILED' AND w.action='posting_exemplar_prepare') FROM orders_exemplar_secrets e JOIN orders_work w ON w.id=e.work_id WHERE e.tenant_id=$1 AND e.moysklad_account=$2 AND e.ozon_account=$3 AND e.work_id=$4 AND e.expires_at>clock_timestamp()`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, workID).Scan(&s.PostingNumber, &s.TargetFingerprint, &s.CredentialRevision, &s.Digest, &raw, &s.Ciphertext, &s.CreatedAt, &s.ExpiresAt, &s.Ready)
	if err != nil {
		return s, ordersError("read protected exact-scope exemplar evidence", err)
	}
	return s, ordersDecode(raw, &s.Public)
}

func (s *Store) OrderExemplarPublic(ctx context.Context, scope orders.Scope, posting string) (orders.ExemplarPublic, error) {
	session, ok := identity.SessionFromContext(ctx)
	if !ok || scope.Validate() != nil {
		return orders.ExemplarPublic{}, orders.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.ExemplarPublic{}, ordersError("begin exemplar evidence inspection", err)
	}
	defer rollbackMerchantMutation(tx)
	if _, err = ordersActor(ctx, tx, scope.Tenant, session.AuditID); err != nil {
		return orders.ExemplarPublic{}, orders.ErrNotFound
	}
	credentials, credentialErr := ordersCredentials(ctx, tx, scope)
	if credentialErr != nil {
		return orders.ExemplarPublic{}, credentialErr
	}
	result, err := readOrderExemplarPublic(ctx, tx, scope, posting, credentials.Ozon)
	if err != nil {
		return result, err
	}
	return result, ordersRowsError("commit exemplar evidence inspection", tx.Commit(ctx))
}

func (s *Store) deleteExpiredOrderExemplars(ctx context.Context, limit int) (int, error) {
	tag, err := s.Pool.Exec(ctx, `WITH expired AS (SELECT work_id FROM orders_exemplar_secrets WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED) DELETE FROM orders_exemplar_secrets s USING expired e WHERE s.work_id=e.work_id`, limit)
	return int(tag.RowsAffected()), ordersRowsError("expire protected exemplar values", err)
}

func readOrderExemplarPublic(ctx context.Context, tx pgx.Tx, scope orders.Scope, posting string, revision int64) (orders.ExemplarPublic, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT e.public_snapshot FROM orders_exemplar_secrets e JOIN orders_work w ON w.id=e.work_id WHERE e.tenant_id=$1 AND e.moysklad_account=$2 AND e.ozon_account=$3 AND e.posting_number=$4 AND w.state='RECONCILED' AND w.action='posting_exemplar_prepare' AND e.credential_revision=$5 AND e.expires_at>clock_timestamp() ORDER BY e.created_at DESC,e.work_id DESC LIMIT 1`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, posting, revision).Scan(&raw)
	if err != nil {
		return orders.ExemplarPublic{}, ordersError("read owned exemplar inspection", err)
	}
	var result orders.ExemplarPublic
	if err = ordersDecode(raw, &result); err != nil {
		return result, err
	}
	var proofID string
	var proofUntil time.Time
	err = tx.QueryRow(ctx, `SELECT e.work_id,LEAST(e.expires_at,(w.work#>>'{receipt,exemplar_evidence,checked_at}')::timestamptz+interval '5 minutes') FROM orders_exemplar_secrets e JOIN orders_work w ON w.id=e.work_id WHERE e.tenant_id=$1 AND e.moysklad_account=$2 AND e.ozon_account=$3 AND e.posting_number=$4 AND e.target_fingerprint=$5 AND e.credential_revision=$6 AND w.state='RECONCILED' AND w.action IN ('posting_exemplar_set','posting_exemplar_prepare') AND w.work#>>'{receipt,exemplar_evidence,packing_ready}'='true' AND e.expires_at>clock_timestamp() AND (w.work#>>'{receipt,exemplar_evidence,checked_at}')::timestamptz>clock_timestamp()-interval '5 minutes' ORDER BY w.finished_at DESC,w.id DESC LIMIT 1`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, posting, result.TargetFingerprint, revision).Scan(&proofID, &proofUntil)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, ordersError("read exemplar packing proof identity", err)
	}
	result.ProofWorkID = proofID
	if proofID != "" {
		result.ProofValidUntil = &proofUntil
	}
	return result, nil
}
