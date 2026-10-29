//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const integrationSchemaDDLKey = "styk-integration-schema-ddl-v1"

type integrationSchemaBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func cleanupIntegrationSchema(t testing.TB, db integrationSchemaBeginner, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := dropIntegrationSchema(ctx, db, schema); err != nil {
		var database *pgconn.PgError
		if errors.As(err, &database) {
			t.Errorf("drop isolated test schema: SQLSTATE=%s", database.Code)
			return
		}
		t.Error("drop isolated test schema: database operation unavailable")
	}
}

// One dedicated transaction holds the database advisory lock through DROP and
// commit. Independent test processes share this lock; pooled sessions cannot
// retain it or accidentally release another cleanup's lock.
func dropIntegrationSchema(ctx context.Context, db integrationSchemaBeginner, schema string) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin schema cleanup: %w", err)
	}
	defer func() {
		rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		rollbackErr := tx.Rollback(rollback)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("rollback schema cleanup: %w", rollbackErr))
		}
	}()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, integrationSchemaDDLKey); err != nil {
		return fmt.Errorf("serialize schema cleanup: %w", err)
	}
	if _, err = tx.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); err != nil {
		return fmt.Errorf("drop isolated schema: %w", err)
	}
	return tx.Commit(ctx)
}

func TestIntegrationSchemaCleanupSerializesAcrossConnections(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	ordersProcessSafety(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner := releaseSchemaConnection(t, ctx, dsn)
	cleaner := releaseSchemaConnection(t, ctx, dsn)
	schema := fmt.Sprintf("release_cleanup_%d", time.Now().UnixNano())
	if _, err := owner.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal("create isolated cleanup regression schema")
	}
	dropped := false
	t.Cleanup(func() {
		if !dropped {
			cleanupIntegrationSchema(t, owner, schema)
		}
	})
	tx := releaseSchemaLock(t, ctx, owner)
	done := make(chan error, 1)
	go func() { done <- dropIntegrationSchema(ctx, cleaner, schema) }()
	blocked := releaseSchemaWaiter(ctx, tx, cleaner.PgConn().PID())
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal("release schema regression advisory lock")
	}
	if err := <-done; err != nil {
		t.Fatalf("serialized schema cleanup failed: %v", err)
	}
	dropped = true
	if !blocked {
		t.Fatal("independent cleanup connection bypassed the shared transaction lock")
	}
	var exists, retainedLock bool
	err := owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1),EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND granted AND pid=$2)`, schema, cleaner.PgConn().PID()).Scan(&exists, &retainedLock)
	if err != nil || exists || retainedLock {
		t.Fatal("serialized cleanup did not drop the isolated schema")
	}
}

func releaseSchemaConnection(t *testing.T, ctx context.Context, dsn string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("open local schema cleanup regression connection")
	}
	t.Cleanup(func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Error("close schema cleanup regression connection")
		}
	})
	return conn
}

func releaseSchemaLock(t *testing.T, ctx context.Context, conn *pgx.Conn) pgx.Tx {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal("begin schema cleanup regression lock")
	}
	t.Cleanup(func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error("rollback schema cleanup regression lock")
		}
	})
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, integrationSchemaDDLKey); err != nil {
		t.Fatal("hold shared schema cleanup regression lock")
	}
	return tx
}

func releaseSchemaWaiter(ctx context.Context, tx pgx.Tx, pid uint32) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND pid=$1)`, pid).Scan(&waiting)
		if err != nil {
			return false
		}
		if waiting {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
