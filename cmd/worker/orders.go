package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orderprovider"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
	"github.com/lilter96/styk/internal/testpublication"
)

type orderCollector struct {
	db        *postgres.Store
	factory   orderprovider.Factory
	protector orderprivacy.Protector
	workerID  string
}

func (c orderCollector) Collect(ctx context.Context, scope orders.Scope) error {
	binding, err := c.factory.ResolveOrder(ctx, scope, "ozon", scope.OzonAccount, 0)
	if err != nil {
		return err
	}
	source, ok := binding.Client.(orderflow.SourceReader)
	if !ok {
		return orders.ErrBlocked
	}
	collector, err := orderflow.NewIngestor(c.db, source, orderflow.IngestionOptions{WorkerID: c.workerID, Protector: c.protector})
	if err != nil {
		return err
	}
	return collector.Collect(ctx, scope)
}

type ordersWork struct {
	runner    *orderflow.Runner
	retention *ordersRetentionWork
	mode      string
	nextCheck time.Time
}

func (w *ordersWork) RunNext(ctx context.Context) (bool, error) {
	if w.retention != nil {
		if _, err := w.retention.RunNext(ctx); err != nil {
			return false, err
		}
	}
	if w.mode != "schedule" {
		worked, err := w.runner.RunNext(ctx)
		if worked || err != nil {
			return worked, err
		}
	}
	if w.mode == "dispatch" || time.Now().Before(w.nextCheck) {
		return false, nil
	}
	w.nextCheck = time.Now().Add(time.Second)
	return false, w.runner.Tick(ctx)
}
func orderAllowed(scope orders.Scope) bool {
	expected := testpublication.Scope()
	return os.Getenv("STYK_TEST_ACCOUNT_ORDERS") == "true" && scope.Tenant == expected.Tenant && scope.MoyskladAccount == expected.MoyskladAccount && scope.OzonAccount == expected.OzonAccount
}
func newOrdersWork(db *postgres.Store, resolver *credentials.Resolver) (application.PublicationRunner, error) {
	retention := &ordersRetentionWork{store: db, now: time.Now, mode: os.Getenv("STYK_WORKER_MODE")}
	if os.Getenv("STYK_TEST_ACCOUNT_ORDERS") != "true" {
		return retention, nil
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, errors.New("order worker identity unavailable")
	}
	id := hex.EncodeToString(token[:])
	gate, err := providercall.New(db, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		return nil, err
	}
	factory := orderprovider.Factory{Credentials: resolver, Gate: gate, DeliveryObserver: db}
	service, err := orderflow.NewService(db, factory, orderflow.ServiceOptions{AllowScope: orderAllowed})
	if err != nil {
		return nil, err
	}
	collector := orderCollector{db: db, factory: factory, protector: orderprivacy.Protector{Vault: resolver.Vault, Store: db}, workerID: id}
	runner, err := orderflow.NewRunner(db, factory, orderflow.RunnerOptions{WorkerID: id, AllowScope: orderAllowed, Service: service, Collector: collector})
	if err != nil {
		return nil, err
	}
	return &ordersWork{runner: runner, retention: retention, mode: os.Getenv("STYK_WORKER_MODE")}, nil
}
func runSchedule(ctx context.Context, runners ...application.PublicationRunner) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		for _, runner := range runners {
			if runner != nil {
				if _, err := runner.RunNext(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("scheduler cycle requires attention")
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
