package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogprovider"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
	"github.com/lilter96/styk/internal/testpublication"
)

// catalogWork shares the normal worker's fair RunNext turn and durable outbox.
// A read-only traversal has its own bounded budget; it never writes to a provider.
type catalogWork struct {
	publisher catalogsync.Runner
	service   catalogsync.Service
	nextCheck time.Time
}

func (w *catalogWork) RunNext(ctx context.Context) (bool, error) {
	ctx = catalogprovider.AuditRequests(ctx)
	worked, err := w.publisher.RunNext(ctx)
	if err != nil || worked || time.Now().Before(w.nextCheck) {
		return worked, err
	}
	w.nextCheck = time.Now().Add(5 * time.Second)
	read, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return w.service.Tick(read)
}

func catalogAllowed(scope catalog.Scope) bool {
	allowed := testpublication.Scope()
	return os.Getenv("STYK_TEST_ACCOUNT_CATALOG") == "true" && scope.Tenant == allowed.Tenant &&
		scope.MoyskladAccount == allowed.MoyskladAccount && scope.OzonAccount == allowed.OzonAccount
}

func newCatalogWork(db *postgres.Store, resolver *credentials.Resolver) (application.PublicationRunner, error) {
	if os.Getenv("STYK_TEST_ACCOUNT_CATALOG") != "true" {
		return nil, nil
	}
	var instance [16]byte
	if _, err := rand.Read(instance[:]); err != nil {
		return nil, errors.New("catalog worker identity unavailable")
	}
	gate, err := providercall.New(db, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		return nil, err
	}
	factory := catalogprovider.Factory{Credentials: resolver, Gate: gate}
	return &catalogWork{
		publisher: catalogsync.Runner{Enabled: true, WorkerID: hex.EncodeToString(instance[:]), Store: db, Resolver: factory, AllowScope: catalogAllowed},
		service:   catalogsync.Service{Store: db, Resolver: factory, AllowScope: catalogAllowed},
	}, nil
}
