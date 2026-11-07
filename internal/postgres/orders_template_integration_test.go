//go:build integration

package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// A template contains only the migrated empty schema. Every test receives a
// distinct database; no mutable rows, sequences, queues or leases are shared.
// Dedicated roles without CREATEDB retain the existing isolated-schema path.
type inventoryTemplate struct {
	once    sync.Once
	admin   *Store
	name    string
	err     error
	enabled bool
}

var inventoryTemplates sync.Map

func TestMain(m *testing.M) {
	code := m.Run()
	inventoryTemplates.Range(func(_, value any) bool {
		template := value.(*inventoryTemplate)
		if template.admin == nil {
			return true
		}
		if template.name != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err := template.admin.Pool.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{template.name}.Sanitize()+" WITH (FORCE)")
			cancel()
			if err != nil {
				fmt.Fprintln(os.Stderr, "isolated inventory template cleanup failed")
				code = 1
			}
		}
		template.admin.Pool.Close()
		return true
	})
	os.Exit(code)
}

func orderInventoryTemplateFixture(t *testing.T, u *url.URL) (*Store, bool) {
	t.Helper()
	copyURL := *u
	query := copyURL.Query()
	query.Del("search_path")
	copyURL.RawQuery = query.Encode()
	value, _ := inventoryTemplates.LoadOrStore(copyURL.String(), &inventoryTemplate{})
	template := value.(*inventoryTemplate)
	template.once.Do(func() { template.err = template.initialize(copyURL) })
	if template.err != nil {
		t.Fatal(template.err)
	}
	if !template.enabled {
		return nil, false
	}
	name := fmt.Sprintf("styk_inventory_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := template.admin.Pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template.name}.Sanitize()); err != nil {
		t.Fatal("clone isolated inventory template")
	}
	t.Cleanup(func() {
		cleanup, finish := context.WithTimeout(context.Background(), 15*time.Second)
		defer finish()
		if _, err := template.admin.Pool.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("drop isolated inventory clone")
		}
	})
	copyURL.Path = "/" + name
	db, err := Open(ctx, copyURL.String(), 15*time.Second)
	if err != nil {
		t.Fatal("open isolated inventory clone")
	}
	t.Cleanup(db.Pool.Close)
	return db, true
}

func (template *inventoryTemplate) initialize(u url.URL) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := Open(ctx, u.String(), 15*time.Second)
	if err != nil {
		return fmt.Errorf("open dedicated inventory template connection")
	}
	template.admin = admin
	if err = admin.Pool.QueryRow(ctx, `SELECT rolcreatedb OR rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&template.enabled); err != nil {
		return fmt.Errorf("check isolated inventory clone permission")
	}
	if !template.enabled {
		return nil
	}
	template.name = fmt.Sprintf("styk_inventory_template_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err = admin.Pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{template.name}.Sanitize()); err != nil {
		return fmt.Errorf("create empty inventory migration template")
	}
	u.Path = "/" + template.name
	if err = runOrderInventoryMigrations(ctx, u.String()); err != nil {
		return err
	}
	if _, err = admin.Pool.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{template.name}.Sanitize()+" ALLOW_CONNECTIONS false"); err != nil {
		return fmt.Errorf("seal empty inventory migration template")
	}
	return nil
}

func runOrderInventoryMigrations(ctx context.Context, dsn string) error {
	args := []string{"run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", dsn, "up"}
	if output, err := exec.CommandContext(ctx, "go", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("inventory attribution migration failed: %s", strings.ReplaceAll(string(output), dsn, "[dedicated test database]"))
	}
	return nil
}
