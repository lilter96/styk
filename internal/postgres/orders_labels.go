package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

func saveOrderLabelArtifact(ctx context.Context, tx pgx.Tx, work orders.Work, c orders.Completion, now time.Time) error {
	if c.LabelArtifact == nil {
		return nil
	}
	a := *c.LabelArtifact
	if c.State != orders.StateReconciled || work.Command.Action != orders.ActionPostingLabelCreate || a.Validate() != nil || a.Scope != work.Scope || a.WorkID != work.ID || a.TaskID != work.Receipt.TaskID {
		return orders.ErrStale
	}
	if work.Command.PostingAction == nil || a.LabelType != work.Command.PostingAction.Parameters.LabelType || a.CreatedAt.After(now.Add(time.Minute)) || !a.ExpiresAt.After(now) {
		return orders.ErrStale
	}
	if c.Receipt.Artifact == nil || !c.Receipt.Artifact.Available || c.Receipt.Artifact.DownloadURL != orders.LabelArtifactURL(work.ID) || c.Receipt.Artifact.LabelType != a.LabelType || !c.Receipt.Artifact.ExpiresAt.Equal(a.ExpiresAt) {
		return orders.ErrStale
	}
	config, err := readOrderConfig(ctx, tx, work.Scope)
	if err != nil {
		return err
	}
	if config.Revision != work.ConfigRevision || a.ExpiresAt.After(a.CreatedAt.Add(config.LabelRetention())) {
		return orders.ErrStale
	}
	args := append([]any{a.WorkID}, ordersScopeArgs(a.Scope)...)
	args = append(args, a.TaskID, a.LabelType, a.Digest, a.Size, a.Ciphertext, a.CreatedAt, a.ExpiresAt)
	_, err = tx.Exec(ctx, `INSERT INTO orders_label_artifacts(work_id,tenant_id,moysklad_account,ozon_account,task_id,label_type,digest,plain_size,ciphertext,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, args...)
	return ordersRowsError("persist encrypted reconciled label", err)
}

func (s *Store) OrderLabelArtifact(ctx context.Context, workID string) (orders.LabelArtifact, error) {
	session, ok := identity.SessionFromContext(ctx)
	if !ok || workID == "" || len(workID) > 512 {
		return orders.LabelArtifact{}, orders.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.LabelArtifact{}, ordersError("begin label read", err)
	}
	defer rollbackMerchantMutation(tx)
	a := orders.LabelArtifact{WorkID: workID}
	err = tx.QueryRow(ctx, `SELECT a.tenant_id,a.moysklad_account,a.ozon_account,a.task_id,a.label_type,a.digest,a.plain_size,a.ciphertext,a.created_at,a.expires_at FROM orders_label_artifacts a JOIN orders_work w ON w.id=a.work_id JOIN saas_merchant_accounts m ON m.tenant_id=a.tenant_id WHERE a.work_id=$1 AND m.user_id=$2 AND w.state='RECONCILED' AND a.expires_at>clock_timestamp()`, workID, session.User.ID).Scan(&a.Scope.Tenant, &a.Scope.MoyskladAccount, &a.Scope.OzonAccount, &a.TaskID, &a.LabelType, &a.Digest, &a.Size, &a.Ciphertext, &a.CreatedAt, &a.ExpiresAt)
	if err != nil {
		return orders.LabelArtifact{}, ordersError("read owned encrypted label", err)
	}
	if _, err = ordersActor(ctx, tx, a.Scope.Tenant, session.AuditID); err != nil {
		return orders.LabelArtifact{}, orders.ErrNotFound
	}
	return a, ordersRowsError("finish owned label read", tx.Commit(ctx))
}

func deleteExpiredOrderLabels(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, limit int) (int, error) {
	tag, err := q.Exec(ctx, `WITH expired AS (SELECT work_id FROM orders_label_artifacts WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED) DELETE FROM orders_label_artifacts a USING expired e WHERE a.work_id=e.work_id`, limit)
	if err != nil {
		return 0, ordersError("expire encrypted labels", err)
	}
	return int(tag.RowsAffected()), nil
}
