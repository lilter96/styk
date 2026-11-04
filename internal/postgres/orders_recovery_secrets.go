package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

func deleteExpiredRecoveryReasons(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, limit int) (int, error) {
	tag, err := q.Exec(ctx, `WITH expired AS (SELECT attestation_id FROM orders_recovery_reason_secrets WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED) DELETE FROM orders_recovery_reason_secrets s USING expired e WHERE s.attestation_id=e.attestation_id`, limit)
	if err != nil {
		return 0, ordersError("expire protected recovery reasons", err)
	}
	return int(tag.RowsAffected()), nil
}
