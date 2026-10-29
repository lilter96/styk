package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) OrderConfig(ctx context.Context, scope orders.Scope) (orders.Config, error) {
	if scope.Validate() != nil {
		return orders.Config{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readOrderConfig(ctx, s.Pool, scope)
}

func readOrderConfig(ctx context.Context, q ordersQuerier, scope orders.Scope) (orders.Config, error) {
	var config orders.Config
	var raw []byte
	err := q.QueryRow(ctx, `SELECT r.config FROM orders_configs c JOIN orders_config_revisions r USING(tenant_id,moysklad_account,ozon_account,revision) WHERE c.tenant_id=$1 AND c.moysklad_account=$2 AND c.ozon_account=$3`, ordersScopeArgs(scope)...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultOrderConfig(ctx, q, scope)
	}
	if err != nil {
		return config, ordersError("read order configuration", err)
	}
	return config, ordersDecode(raw, &config)
}

func defaultOrderConfig(ctx context.Context, q ordersQuerier, scope orders.Scope) (orders.Config, error) {
	var count int
	err := q.QueryRow(ctx, `SELECT count(*) FROM stock_connections WHERE tenant_id=$1 AND ((source='moysklad' AND account_id=$2) OR (source='ozon' AND account_id=$3))`, ordersScopeArgs(scope)...).Scan(&count)
	if err != nil {
		return orders.Config{}, ordersError("read order connection ownership", err)
	}
	if count != 2 {
		return orders.Config{}, orders.ErrNotFound
	}
	return orders.Config{Scope: scope, IntervalSeconds: 300, SourceFreshnessSeconds: 300, PreviewTTLSeconds: 300, FieldMappings: []orders.FieldMapping{}, StatusMappings: []orders.StatusMapping{}}, nil
}

func (s *Store) SaveOrderConfig(ctx context.Context, req orders.SaveConfigRequest, _ time.Time) (orders.Config, error) {
	if req.Config.Scope.Validate() != nil || req.ExpectedRevision < 0 || !ordersRequestKey(req.IdempotencyKey) {
		return orders.Config{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.Config{}, ordersError("begin order configuration", err)
	}
	defer rollbackMerchantMutation(tx)
	owner, err := ordersActor(ctx, tx, req.Config.Scope.Tenant, req.Actor)
	if err != nil {
		return orders.Config{}, err
	}
	config, err := saveOrderConfigLocked(ctx, tx, req, owner)
	if err != nil {
		return orders.Config{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return orders.Config{}, ordersError("commit order configuration", err)
	}
	return config, nil
}

func saveOrderConfigLocked(ctx context.Context, tx pgx.Tx, req orders.SaveConfigRequest, owner string) (orders.Config, error) {
	if err := ordersLock(ctx, tx, req.Config.Scope); err != nil {
		return orders.Config{}, err
	}
	if err := ordersCredentialLocks(ctx, tx, req.Config.Scope); err != nil {
		return orders.Config{}, err
	}
	hash, err := ordersHash(struct {
		Request orders.SaveConfigRequest
		Actor   string
	}{req, req.Actor})
	if err != nil {
		return orders.Config{}, err
	}
	prior, err := replayOrderConfig(ctx, tx, req, hash)
	if err == nil {
		return prior, nil
	}
	if !errors.Is(err, orders.ErrNotFound) {
		return orders.Config{}, err
	}
	return insertOrderConfig(ctx, tx, req, owner, hash)
}

func replayOrderConfig(ctx context.Context, tx pgx.Tx, req orders.SaveConfigRequest, hash string) (orders.Config, error) {
	var config orders.Config
	var raw []byte
	var previous string
	args := append(ordersScopeArgs(req.Config.Scope), req.IdempotencyKey)
	err := tx.QueryRow(ctx, `SELECT config,request_hash FROM orders_config_revisions WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND request_key=$4`, args...).Scan(&raw, &previous)
	if err != nil {
		return config, ordersError("read order configuration replay", err)
	}
	if previous != hash {
		return config, orders.ErrConflict
	}
	return config, ordersDecode(raw, &config)
}

func insertOrderConfig(ctx context.Context, tx pgx.Tx, req orders.SaveConfigRequest, owner, hash string) (orders.Config, error) {
	current, err := readOrderConfig(ctx, tx, req.Config.Scope)
	if err != nil {
		return orders.Config{}, err
	}
	if current.Revision != req.ExpectedRevision {
		return orders.Config{}, orders.ErrStale
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return orders.Config{}, err
	}
	config, err := finalizedOrderConfig(req.Config, current, owner, now)
	if err != nil {
		return orders.Config{}, err
	}
	if err = writeOrderConfig(ctx, tx, req, config, owner, hash); err != nil {
		return orders.Config{}, err
	}
	return config, nil
}

func finalizedOrderConfig(config, previous orders.Config, owner string, now time.Time) (orders.Config, error) {
	config.Revision = previous.Revision + 1
	config.UpdatedAt = now
	if config.Automation != nil {
		grant := *config.Automation
		grant.Scope, grant.OwnerID, grant.SourceAccount = config.Scope, owner, config.Scope.OzonAccount
		grant.ConfigRevision, grant.Revision, grant.GrantedAt = config.Revision, config.Revision, now
		grant.ID = ""
		if previous.Automation != nil {
			grant.ID = previous.Automation.ID
		}
		if grant.ID == "" {
			var err error
			grant.ID, err = ordersID()
			if err != nil {
				return config, err
			}
		}
		config.Automation = &grant
	}
	if err := config.Validate(); err != nil {
		return config, err
	}
	return config, nil
}

func writeOrderConfig(ctx context.Context, tx pgx.Tx, req orders.SaveConfigRequest, c orders.Config, owner, hash string) error {
	raw, err := ordersJSON(c)
	if err != nil {
		return err
	}
	args := append(ordersScopeArgs(c.Scope), c.Revision, c.Enabled, c.UpdatedAt)
	if _, err = tx.Exec(ctx, `INSERT INTO orders_configs(tenant_id,moysklad_account,ozon_account,revision,enabled,next_run_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6) ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET revision=EXCLUDED.revision,enabled=EXCLUDED.enabled,next_run_at=EXCLUDED.next_run_at,updated_at=EXCLUDED.updated_at`, args...); err != nil {
		return ordersError("save order configuration head", err)
	}
	args = append(ordersScopeArgs(c.Scope), c.Revision, raw, req.Actor, owner, req.IdempotencyKey, hash, c.UpdatedAt)
	if _, err = tx.Exec(ctx, `INSERT INTO orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision,config,actor,owner_user_id,request_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, args...); err != nil {
		return ordersError("save order configuration revision", err)
	}
	return insertOrderGrant(ctx, tx, c)
}

func insertOrderGrant(ctx context.Context, tx pgx.Tx, config orders.Config) error {
	grant := config.Automation
	if grant == nil {
		return nil
	}
	raw, err := ordersJSON(grant)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_automation_grants(id,revision,tenant_id,moysklad_account,ozon_account,config_revision,owner_user_id,grant_record,granted_at,expires_at,revoked_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, grant.ID, grant.Revision, config.Scope.Tenant, config.Scope.MoyskladAccount, config.Scope.OzonAccount, config.Revision, grant.OwnerID, raw, grant.GrantedAt, grant.ExpiresAt, grant.RevokedAt)
	if err != nil {
		return ordersError("save order automation grant", err)
	}
	return nil
}

func (s *Store) DueOrderConfigs(ctx context.Context, _ time.Time, limit int) ([]orders.Config, error) {
	if limit < 1 || limit > 100 {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, ordersError("begin order scheduler", err)
	}
	defer rollbackMerchantMutation(tx)
	rows, err := tx.Query(ctx, `SELECT r.config FROM orders_configs c JOIN orders_config_revisions r USING(tenant_id,moysklad_account,ozon_account,revision) WHERE c.enabled AND c.next_run_at<=clock_timestamp() ORDER BY c.next_run_at,c.tenant_id LIMIT $1 FOR UPDATE OF c SKIP LOCKED`, limit)
	if err != nil {
		return nil, ordersError("select due order configurations", err)
	}
	configs, err := scanOrderConfigs(rows)
	if err != nil {
		return nil, err
	}
	if err = advanceOrderSchedules(ctx, tx, configs); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, ordersError("commit order schedule", err)
	}
	return configs, nil
}

func scanOrderConfigs(rows pgx.Rows) ([]orders.Config, error) {
	defer rows.Close()
	configs := make([]orders.Config, 0, 10)
	for rows.Next() {
		var raw []byte
		var config orders.Config
		if err := rows.Scan(&raw); err != nil {
			return nil, ordersError("scan order configuration", err)
		}
		if err := ordersDecode(raw, &config); err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	return configs, ordersRowsError("read order configurations", rows.Err())
}

func advanceOrderSchedules(ctx context.Context, tx pgx.Tx, configs []orders.Config) error {
	for _, config := range configs {
		args := append(ordersScopeArgs(config.Scope), config.IntervalSeconds)
		if _, err := tx.Exec(ctx, `UPDATE orders_configs SET next_run_at=clock_timestamp()+$4*interval '1 second' WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, args...); err != nil {
			return ordersError("advance order schedule", err)
		}
	}
	return nil
}

func ordersRowsError(action string, err error) error {
	if err != nil {
		return ordersError(action, err)
	}
	return nil
}

func orderAutomationActor(grant orders.AutomationGrant) string {
	return fmt.Sprintf("AUTOMATION:%s:%d", grant.ID, grant.Revision)
}
