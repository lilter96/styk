// Package devseed loads synthetic observations only into a marked local demo database.
package devseed

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const Database = "styk_demo_dev"
const Marker = "STYK isolated development demo v1"

//go:embed sql/*.sql
var fixtures embed.FS

type Config struct {
	Environment string
	URL         string
	Seed        int64
	AsOf        time.Time
}

type Report struct {
	Tenant   string `json:"tenant"`
	Existing bool   `json:"existing"`
}

func (c Config) Validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || c.Environment != "development" || u.Path != "/"+Database || c.AsOf.IsZero() || c.Seed < 0 || c.Seed > 1_000_000_000 || !c.AsOf.Equal(c.AsOf.Truncate(time.Second)) {
		return errors.New("seed requires development, a local styk_demo_dev database, nonnegative seed and explicit as-of")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "postgres" && u.Scheme != "postgresql" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return errors.New("seed refuses non-loopback database hosts")
	}
	for name := range u.Query() {
		if name != "sslmode" {
			return errors.New("seed refuses database routing or session overrides")
		}
	}
	return nil
}

func Run(ctx context.Context, pool *pgxpool.Pool, c Config) (Report, error) {
	if err := c.Validate(); err != nil {
		return Report{}, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("begin demo fixture: %w", err)
	}
	defer rollback(ctx, tx)
	if err = guard(ctx, tx); err != nil {
		return Report{}, err
	}
	report, err := demoOwner(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	report.Existing, err = populate(ctx, tx, report.Tenant, c)
	if err != nil {
		return Report{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit demo fixture: %w", err)
	}
	return report, nil
}

func rollback(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		// The caller already reports the operation failure; rollback has no secrets.
		fmt.Println("demo fixture rollback did not complete")
	}
}

func guard(ctx context.Context, tx pgx.Tx) error {
	var database, marker string
	if err := tx.QueryRow(ctx, `SELECT current_database(),coalesce(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=current_database()`).Scan(&database, &marker); err != nil {
		return fmt.Errorf("inspect demo database: %w", err)
	}
	if database != Database || marker != Marker {
		return errors.New("database is not explicitly marked as the isolated demo")
	}
	if _, err := tx.Exec(ctx, `SET LOCAL search_path=public`); err != nil {
		return fmt.Errorf("set fixture schema: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(784513290)`); err != nil {
		return fmt.Errorf("lock demo fixture: %w", err)
	}
	var unsafe bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_users WHERE email<>'demo@styk.local') OR EXISTS(SELECT 1 FROM connection_credentials) OR EXISTS(SELECT 1 FROM stock_connections WHERE account_id NOT LIKE 'STYK-DEMO-%')`).Scan(&unsafe)
	if err != nil {
		return fmt.Errorf("inspect demo ownership: %w", err)
	}
	if unsafe {
		return errors.New("refusing to seed a database with other users or external credentials/accounts")
	}
	return nil
}

func demoOwner(ctx context.Context, tx pgx.Tx) (Report, error) {
	var report Report
	err := tx.QueryRow(ctx, `SELECT a.tenant_id FROM saas_merchant_accounts a JOIN saas_users u ON u.id=a.user_id JOIN saas_memberships m ON (m.workspace_id,m.user_id)=(a.tenant_id,a.user_id) WHERE u.email='demo@styk.local' AND u.email_verified_at IS NOT NULL AND u.disabled_at IS NULL AND m.role='OWNER' AND m.revoked_at IS NULL`).Scan(&report.Tenant)
	if err != nil {
		return report, fmt.Errorf("verified demo owner must register first: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_workspaces SET name='STYK Demo Company' WHERE id=$1`, report.Tenant); err != nil {
		return report, fmt.Errorf("name demo organization: %w", err)
	}
	return report, nil
}

func populate(ctx context.Context, tx pgx.Tx, tenant string, c Config) (bool, error) {
	stamp := fmt.Sprintf("v1:%d:%s", c.Seed, c.AsOf.UTC().Format(time.RFC3339))
	var previous string
	err := tx.QueryRow(ctx, `SELECT target FROM saas_security_events WHERE workspace_id=$1 AND action='DEMO_SEED'`, tenant).Scan(&previous)
	if err == nil && previous == stamp {
		return true, checkCounts(ctx, tx, tenant)
	}
	if err == nil {
		return false, errors.New("different seed already exists; use the guarded backup/reset workflow")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("read seed marker: %w", err)
	}
	var occupied bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM source_imports)`).Scan(&occupied); err != nil {
		return false, fmt.Errorf("inspect existing observations: %w", err)
	}
	if occupied {
		return false, errors.New("existing observations lack a seed marker; refusing to overwrite")
	}
	for _, name := range []string{"connections", "products", "catalog", "mappings", "orders", "events"} {
		if err = execute(ctx, tx, name, tenant, c); err != nil {
			return false, err
		}
	}
	if err = checkCounts(ctx, tx, tenant); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO saas_security_events(workspace_id,action,target) VALUES($1,'DEMO_SEED',$2)`, tenant, stamp)
	if err != nil {
		return false, fmt.Errorf("record seed marker: %w", err)
	}
	return false, nil
}

func execute(ctx context.Context, tx pgx.Tx, name, tenant string, c Config) error {
	data, err := fixtures.ReadFile("sql/" + name + ".sql")
	if err != nil {
		return fmt.Errorf("read %s fixture: %w", name, err)
	}
	for _, statement := range strings.Split(string(data), "-- statement") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err = tx.Exec(ctx, statement, tenant, c.Seed, c.AsOf.UTC()); err != nil {
			return fmt.Errorf("load %s fixture: %w", name, err)
		}
	}
	return nil
}

func checkCounts(ctx context.Context, tx pgx.Tx, tenant string) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM source_entities WHERE kind='product')=500
 AND (SELECT count(*) FROM ozon_product_observations)=2500
 AND (SELECT count(*) FROM catalog_sync_snapshots)=2500
 AND (SELECT count(*) FROM ozon_posting_observations WHERE tenant_id=$1)=1000
 AND (SELECT count(*) FROM moy_order_observations WHERE tenant_id=$1)=1000
 AND (SELECT count(*) FROM product_mappings WHERE tenant_id=$1)=1960
 AND (SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1)=15
 AND (SELECT count(*) FROM stock_connections WHERE tenant_id=$1)=6
 AND (SELECT count(*) FROM source_warehouse_stock_observations)=1500
 AND (SELECT count(*) FROM ozon_fbs_stock_observations)=7500
 AND (SELECT count(*) FROM outbox_tasks)=22`, tenant).Scan(&valid)
	if err != nil {
		return fmt.Errorf("verify fixture counts: %w", err)
	}
	if !valid {
		return errors.New("fixture count mismatch; no changes committed")
	}
	return nil
}
