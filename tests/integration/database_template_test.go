//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/postgres"
)

// A template contains only the migrated empty schema. Every test receives a
// distinct database; no mutable rows, sequences, queues or leases are shared.
// Dedicated roles without CREATEDB retain the existing isolated-schema path.
type catalogTemplate struct {
	once    sync.Once
	admin   *postgres.Store
	name    string
	err     error
	enabled bool
}

var catalogTemplates sync.Map

func TestMain(m *testing.M) {
	code := m.Run()
	catalogTemplates.Range(func(_, value any) bool {
		template := value.(*catalogTemplate)
		if template.admin == nil {
			return true
		}
		if template.name != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err := template.admin.Pool.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{template.name}.Sanitize()+" WITH (FORCE)")
			cancel()
			if err != nil {
				fmt.Fprintln(os.Stderr, "isolated catalog template cleanup failed")
				code = 1
			}
		}
		template.admin.Pool.Close()
		return true
	})
	if err := cleanupIntegrationGoose(); err != nil {
		fmt.Fprintln(os.Stderr, "private migration CLI cleanup failed")
		code = 1
	}
	os.Exit(code)
}

func catalogTemplateFixture(t testing.TB, u *url.URL) (*postgres.Store, bool) {
	t.Helper()
	copyURL := *u
	query := copyURL.Query()
	query.Del("search_path")
	copyURL.RawQuery = query.Encode()
	value, _ := catalogTemplates.LoadOrStore(copyURL.String(), &catalogTemplate{})
	template := value.(*catalogTemplate)
	template.once.Do(func() { template.err = template.initialize(copyURL) })
	if template.err != nil {
		t.Fatal(template.err)
	}
	if !template.enabled {
		return nil, false
	}
	name := fmt.Sprintf("styk_orders_clone_%d_%d", os.Getpid(), time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := template.admin.Pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template.name}.Sanitize()); err != nil {
		t.Fatal("clone isolated catalog template")
	}
	t.Cleanup(func() {
		cleanup, finish := context.WithTimeout(context.Background(), 15*time.Second)
		defer finish()
		if _, err := template.admin.Pool.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("drop isolated catalog clone")
		}
	})
	copyURL.Path = "/" + name
	db, err := postgres.Open(ctx, copyURL.String(), 15*time.Second)
	if err != nil {
		t.Fatal("open isolated catalog clone")
	}
	t.Cleanup(db.Pool.Close)
	return db, true
}

func (template *catalogTemplate) initialize(u url.URL) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := postgres.Open(ctx, u.String(), 15*time.Second)
	if err != nil {
		return fmt.Errorf("open dedicated catalog template connection")
	}
	template.admin = admin
	if err = admin.Pool.QueryRow(ctx, `SELECT rolcreatedb OR rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&template.enabled); err != nil {
		return fmt.Errorf("check isolated catalog clone permission")
	}
	if !template.enabled {
		return nil
	}
	template.name = fmt.Sprintf("styk_orders_template_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err = admin.Pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{template.name}.Sanitize()); err != nil {
		return fmt.Errorf("create empty catalog migration template")
	}
	u.Path = "/" + template.name
	if err = runCatalogTemplateMigrations(ctx, u.String()); err != nil {
		return err
	}
	if _, err = admin.Pool.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{template.name}.Sanitize()+" ALLOW_CONNECTIONS false"); err != nil {
		return fmt.Errorf("seal empty catalog migration template")
	}
	return nil
}

func runCatalogTemplateMigrations(ctx context.Context, dsn string) error {
	command, err := newGooseCommand(ctx, "-dir", "../../migrations", "postgres", dsn, "up")
	if err != nil {
		return err
	}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("catalog fixture migration failed: %s", strings.ReplaceAll(string(output), dsn, "[dedicated test database]"))
	}
	return nil
}
