package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
)

func stockReviewLockKeys(scope domain.StockSyncScope) []string {
	return []string{"ozon:" + scope.OzonAccount, "moysklad:" + scope.MoyskladAccount, "matching:" + scope.Tenant, "warehouse-mapping:" + scope.Tenant}
}

// Acquire session locks before beginning the repeatable-read transaction. A
// transaction snapshot taken before a lock wait can miss the import that held it.
func (s *Store) lockDispatchConnection(ctx context.Context, scope domain.StockSyncScope) (*pgxpool.Conn, []string, error) {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire dispatch connection: %w", err)
	}
	// Import commits lock credentials before their source account; use that
	// order here too, including when a credential row does not yet exist.
	keys := make([]string, 0, 7)
	keys = append(keys, syncKey(scope), credentials.ScopeKey(credentials.Scope{Provider: "ozon", Account: scope.OzonAccount}), credentials.ScopeKey(credentials.Scope{Provider: "moysklad", Account: scope.MoyskladAccount}))
	keys = append(keys, stockReviewLockKeys(scope)...)
	for _, key := range keys {
		if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
			// A canceled query can have acquired its lock despite an error. Closing
			// the connection releases every lock instead of returning it to the pool.
			discardDispatchConnection(conn)
			return nil, nil, fmt.Errorf("lock dispatch scope: %w", err)
		}
	}
	return conn, keys, nil
}

func releaseDispatchConnection(conn *pgxpool.Conn, keys []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := len(keys) - 1; i >= 0; i-- {
		var unlocked bool
		if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, keys[i]).Scan(&unlocked); err != nil {
			discardDispatchConnection(conn)
			return fmt.Errorf("release dispatch lock: %w", err)
		}
		if !unlocked {
			discardDispatchConnection(conn)
			return errors.New("dispatch lock was not held")
		}
	}
	conn.Release()
	return nil
}

func discardDispatchConnection(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Hijack().Close(ctx); err != nil {
		slog.Error("dispatch connection close unavailable")
	}
}

func rollbackDispatch(tx pgx.Tx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return fmt.Errorf("rollback dispatch review: %w", err)
	}
	return nil
}

func dispatchClock(ctx context.Context, tx pgx.Tx, notBefore time.Time) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("read dispatch clock: %w", err)
	}
	if notBefore.After(now) {
		return notBefore, nil
	}
	return now, nil
}

func controlledDispatchClaim(ctx context.Context, tx pgx.Tx, op domain.StockPublication, now time.Time) (bool, error) {
	var owned bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbox_tasks WHERE publication_operation_id=$1 AND task_type='stock_publication' AND state='running' AND claim_token=$2 AND lease_until>$3 AND lease_until>clock_timestamp())`, op.ID, op.OutboxClaimToken, now).Scan(&owned)
	if err != nil {
		return false, fmt.Errorf("check dispatch claim: %w", err)
	}
	return owned, nil
}

func dispatchReviewUnexpired(frozen, current domain.StockSyncReview, now time.Time) bool {
	if !frozen.ExpiresAt.After(now) {
		return false
	}
	for _, target := range current.Targets {
		if target.Decision.ExpiresAt == nil || !target.Decision.ExpiresAt.After(now) {
			return false
		}
	}
	return true
}
