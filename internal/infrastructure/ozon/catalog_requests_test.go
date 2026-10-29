package ozon

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogRequestObserverRecordsEachReadDispatchOnce(t *testing.T) {
	t.Parallel()
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !catalogFixtureRead(t, w, r) {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	events := make([]catalog.RequestEvent, 0, 3)
	ctx := catalog.WithRequestObserver(context.Background(), func(event catalog.RequestEvent) { events = append(events, event) })
	_, err := client.ReadCatalog(ctx, catalog.ReadRequest{Scope: catalogFixtureScope, Account: catalogFixtureIdentity().Account, Identities: []catalog.Identity{catalogFixtureIdentity()}, Limit: 1})
	if err != nil || len(events) != 3 {
		t.Fatalf("reads=%d err=%v", len(events), err)
	}
	for index, path := range []string{productInfoPath, catalogAttributesPath, catalogDescriptionPath} {
		if events[index] != (catalog.RequestEvent{Provider: catalog.ProviderOzon, Method: http.MethodPost, Operation: path, Status: 200}) {
			t.Fatalf("request %d leaked or lost attempt details: %+v", index, events[index])
		}
	}
}

func TestCatalogRequestObserverRecordsOneFailedWriteAttempt(t *testing.T) {
	t.Parallel()
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if catalogFixtureRead(t, w, r) {
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	command := catalogFixtureCommand()
	command.ExpectedTargetRevision = fixtureCatalogSnapshot(t, client).Products[0].Revision
	if err := client.BindCatalogIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	events := make([]catalog.RequestEvent, 0, 5)
	ctx := catalog.WithRequestObserver(context.Background(), func(event catalog.RequestEvent) { events = append(events, event) })
	receipt, err := client.WriteCatalog(ctx, command)
	if !errors.Is(err, ErrCatalogWriteUnknown) || receipt.State != catalog.ReceiptUnknown || len(events) != 5 {
		t.Fatalf("events=%d receipt=%+v err=%v", len(events), receipt, err)
	}
	writes := 0
	for _, event := range events {
		if event.Operation == catalogPatchPath {
			writes++
			if event.Status != 503 || event.Interrupted {
				t.Fatalf("HTTP failure misclassified: %+v", event)
			}
		}
	}
	if writes != 1 {
		t.Fatalf("mutation emitted %d dispatch events", writes)
	}
}

func TestCatalogRequestObserverRecordsTransportInterruptionWithoutSecrets(t *testing.T) {
	t.Parallel()
	options := catalogFixtureOptions("https://example.invalid")
	options.Attempts = 1
	options.HTTPClient = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("fixture-sensitive-url-and-key") })}
	client, err := NewCatalogClient(options, catalogFixtureScope)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]catalog.RequestEvent, 0, 1)
	ctx := catalog.WithRequestObserver(context.Background(), func(event catalog.RequestEvent) { events = append(events, event) })
	_, err = client.ReadCatalog(ctx, catalog.ReadRequest{Scope: catalogFixtureScope, Account: catalogFixtureIdentity().Account, Identities: []catalog.Identity{catalogFixtureIdentity()}, Limit: 1})
	wanted := catalog.RequestEvent{Provider: catalog.ProviderOzon, Method: http.MethodPost, Operation: productInfoPath, Interrupted: true}
	if !errors.Is(err, ErrTransport) || len(events) != 1 || events[0] != wanted {
		t.Fatalf("unsafe interruption observation: %+v", events)
	}
	observeCatalogRequest(context.Background(), productInfoPath, nil, nil)
}
