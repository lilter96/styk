//go:build integration

package integration

import (
	"context"
	"errors"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"os"
	"sync"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL must point to a dedicated migrated test database")
	}
	ctx := context.Background()
	s, err := postgres.Open(ctx, url, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Pool.Close)
	// Use unique keys and never truncate the database.
	key := time.Now().Format("20060102150405.000000000")
	o := domain.Observation{OperationKey: key, Source: "test", SKU: "sku", Quantity: 2}
	id, err := s.Submit(ctx, o, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool.Exec(context.Background(), "DELETE FROM outbox_tasks WHERE operation_key=$1", key)
		_, _ = s.Pool.Exec(context.Background(), "DELETE FROM inventory_observations WHERE operation_key=$1", key)
	})
	id2, err := s.Submit(ctx, o, 3)
	if err != nil || id2 != id {
		t.Fatalf("duplicate: %d %v", id2, err)
	}
	o.Quantity = 3
	if _, err = s.Submit(ctx, o, 3); err == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	var count int
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM outbox_tasks WHERE operation_key=$1", key).Scan(&count); err != nil || count != 1 {
		t.Fatalf("outbox count %d: %v", count, err)
	}
	// Rollback proves the observation and outbox are transactional.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var rollbackID int64
	err = tx.QueryRow(ctx, "INSERT INTO inventory_observations(operation_key,source,sku,quantity) VALUES($1,'test','x',1) RETURNING id", key+"-rollback").Scan(&rollbackID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, "INSERT INTO outbox_tasks(operation_key,observation_id,max_attempts) VALUES($1,$2,3)", key+"-rollback", rollbackID)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM inventory_observations WHERE id=$1", rollbackID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback failed: %d %v", count, err)
	}
	var wg sync.WaitGroup
	results := make(chan *domain.Task, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); task, e := s.Claim(ctx, 100*time.Millisecond); results <- task; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first *domain.Task
	for task := range results {
		if task != nil {
			if first != nil {
				t.Fatal("task claimed twice")
			}
			first = task
		}
	}
	if first == nil || first.OperationKey != key {
		t.Fatal("task not claimed")
	}
	// Simulate a worker dying after claim and recover after lease expiration.
	time.Sleep(150 * time.Millisecond)
	second, err := s.Claim(ctx, time.Second)
	if err != nil || second == nil {
		t.Fatalf("recovery: %v", err)
	}
	if err = s.Finish(ctx, *first, "stale", nil, 0); err == nil {
		t.Fatal("stale worker completion accepted")
	}
	if err = s.Finish(ctx, *second, "", errors.New("mock failure"), 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	third, err := s.Claim(ctx, time.Second)
	if err != nil || third == nil || third.Attempts != 3 {
		t.Fatalf("retry: %+v %v", third, err)
	}
	if err = s.Finish(ctx, *third, "", errors.New("final failure"), 0); err != nil {
		t.Fatal(err)
	}
	var state, msg string
	if err = s.Pool.QueryRow(ctx, "SELECT state,last_error FROM outbox_tasks WHERE id=$1", third.ID).Scan(&state, &msg); err != nil || state != "failed" || msg != "final failure" {
		t.Fatalf("state %s error %s: %v", state, msg, err)
	}
}
