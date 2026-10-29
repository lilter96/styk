package ozon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogValidationReadsWithoutBindingAndReturnsRequiredFacts(t *testing.T) {
	t.Parallel()
	var reads, writes atomic.Int32
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !catalogCreationReads(t, w, r, false, &reads) {
			writes.Add(1)
		}
	})
	command := catalogFixtureCreate()
	delete(command.Extensions, "vat")
	delete(command.Values, catalog.FieldDimensions)
	problems, err := client.ValidateCatalog(context.Background(), command)
	if err != nil || len(problems) != 2 || reads.Load() != 0 {
		t.Fatalf("missing facts: %+v %v reads=%d", problems, err, reads.Load())
	}
	command = catalogFixtureCreate()
	command.Values[catalog.FieldAttributes] = catalogRaw([]CatalogAttribute{})
	problems, err = client.ValidateCatalog(context.Background(), command)
	if err != nil || len(problems) != 1 || problems[0].Code != "REQUIRED_ATTRIBUTE_MISSING" || !strings.Contains(problems[0].Message, "123") {
		t.Fatalf("required attribute lost: %+v %v", problems, err)
	}
	if writes.Load() != 0 || client.intent != nil {
		t.Fatal("readonly validation bound or dispatched an intent")
	}
}

type catalogCloseFailure struct{ io.Reader }

func (catalogCloseFailure) Close() error { return errors.New("fixture close failure") }

func TestCatalogCompleteTaskAcknowledgementSurvivesCloseFailure(t *testing.T) {
	t.Parallel()
	response := &http.Response{StatusCode: 200, Body: catalogCloseFailure{Reader: strings.NewReader(`{"task_id":91}`)}}
	data, status, err := readCatalogMutation(response)
	if err != nil || status != 200 {
		t.Fatalf("complete acknowledgement lost: status=%d %v", status, err)
	}
	if task, err := catalogTaskID(catalogPatchPath, data); err != nil || task != "91" {
		t.Fatalf("task=%s err=%v", task, err)
	}
}

func TestCatalogConcurrentWritersConsumeOneExactIntent(t *testing.T) {
	t.Parallel()
	var writes, authorizations atomic.Int32
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if catalogFixtureRead(t, w, r) {
			return
		}
		writes.Add(1)
		replyCatalogJSON(t, w, map[string]any{"task_id": 91})
	})
	command := catalogFixtureCommand()
	command.ExpectedTargetRevision = fixtureCatalogSnapshot(t, client).Products[0].Revision
	if err := client.BindCatalogIntent(command, func(context.Context) (bool, error) { authorizations.Add(1); return true, nil }); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsFound := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := client.WriteCatalog(context.Background(), command); errorsFound <- err })
	}
	wg.Wait()
	close(errorsFound)
	accepted, denied := 0, 0
	for err := range errorsFound {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrCatalogWritesDisabled) {
			denied++
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if accepted != 1 || denied != 1 || writes.Load() != 1 || authorizations.Load() != 1 {
		t.Fatalf("accepted=%d denied=%d writes=%d authorized=%d", accepted, denied, writes.Load(), authorizations.Load())
	}
}

func TestCatalogUnknownOutcomeAfterResponseIsLost(t *testing.T) {
	t.Parallel()
	var writes atomic.Int32
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if catalogFixtureRead(t, w, r) {
			return
		}
		writes.Add(1)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("fixture transport cannot hijack")
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	command := catalogFixtureCommand()
	command.ExpectedTargetRevision = fixtureCatalogSnapshot(t, client).Products[0].Revision
	if err := client.BindCatalogIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteCatalog(context.Background(), command)
	if !errors.Is(err, ErrCatalogWriteUnknown) || receipt.State != catalog.ReceiptUnknown || writes.Load() != 1 {
		t.Fatalf("lost response retried or settled: %+v %v writes=%d", receipt, err, writes.Load())
	}
}
