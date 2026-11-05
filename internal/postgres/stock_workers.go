package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

func (s *Store) expiredStockScopes(ctx context.Context, now time.Time) ([]domain.StockSyncScope, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT p.tenant_id,p.moysklad_account,p.ozon_account FROM stock_publications p JOIN outbox_tasks q ON q.publication_operation_id=p.id WHERE q.state='running' AND q.lease_until<=$1 ORDER BY p.tenant_id,p.moysklad_account,p.ozon_account LIMIT 25`, now)
	if err != nil {
		return nil, fmt.Errorf("read expired stock scopes: %w", err)
	}
	defer rows.Close()
	scopes := make([]domain.StockSyncScope, 0, 25)
	for rows.Next() {
		var scope domain.StockSyncScope
		if err = rows.Scan(&scope.Tenant, &scope.MoyskladAccount, &scope.OzonAccount); err != nil {
			return nil, fmt.Errorf("read expired stock scope: %w", err)
		}
		scopes = append(scopes, scope)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("finish expired stock scopes: %w", err)
	}
	return scopes, nil
}

func (s *Store) touchStockWorkers(ctx context.Context, workerID string, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	_, err := s.Pool.Exec(ctx, `INSERT INTO stock_test_worker_presence(tenant_id,moysklad_account,ozon_account,worker_id,expires_at) SELECT tenant_id,moysklad_account,ozon_account,$1,$2 FROM stock_sync_settings WHERE enabled ON CONFLICT(tenant_id,moysklad_account,ozon_account,worker_id) DO UPDATE SET expires_at=EXCLUDED.expires_at`, workerID, now.Add(15*time.Second))
	if err != nil {
		return fmt.Errorf("record stock worker presence: %w", err)
	}
	return nil
}
