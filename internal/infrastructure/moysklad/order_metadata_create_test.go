package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

type metadataCreateSimulator struct {
	server         *httptest.Server
	intent         orders.MetadataCreationIntent
	mode           string
	writes         atomic.Int32
	reads          atomic.Int32
	attributePages atomic.Int32
	created        atomic.Bool
	visible        atomic.Bool
	captured       chan []map[string]any
}

func newMetadataCreateSimulator(t *testing.T, mode string) (*metadataCreateSimulator, *OrderClient) {
	t.Helper()
	id := "0123456789abcdef0123456789abcdef"
	scope := orders.Scope{Tenant: "tenant", MoyskladAccount: orderTestID(1), OzonAccount: "ozon-account"}
	fixture := &metadataCreateSimulator{mode: mode, captured: make(chan []map[string]any, 2), intent: orders.MetadataCreationIntent{ID: id, Scope: scope, UniqueCode: "styk-order-status", Type: "string", Marker: "styk-order-attribute-" + id, State: orders.StateDispatching, IdempotencyKey: "setup-status", CreatedAt: time.Now().UTC(), DispatchPermitted: true}}
	fixture.visible.Store(mode != "lag")
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	base, err := New(Options{BaseURL: fixture.server.URL + "/api/remap/1.2/", Token: "synthetic", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 3, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewOrderClient(base, scope)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, client
}

func (fixture *metadataCreateSimulator) attribute() map[string]any {
	return map[string]any{"meta": map[string]any{"href": fixture.server.URL + "/api/remap/1.2/entity/customerorder/metadata/attributes/" + orderTestID(20), "type": "attributemetadata", "mediaType": "application/json"}, "id": orderTestID(20), "name": fixture.intent.Marker, "type": fixture.intent.Type, "required": false}
}

func (fixture *metadataCreateSimulator) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/remap/1.2/")
	writer.Header().Set("Content-Type", "application/json")
	if request.Method == http.MethodGet && path == "context/employee" {
		fixture.reply(writer, map[string]any{"accountId": fixture.intent.Scope.MoyskladAccount})
		return
	}
	if request.Method == http.MethodGet && path == "entity/customerorder/metadata" {
		fixture.reads.Add(1)
		rows := make([]any, 0)
		if fixture.created.Load() && fixture.visible.Load() {
			rows = append(rows, fixture.attribute())
		}
		if fixture.mode == "duplicate" {
			rows = append(rows, fixture.attribute(), fixture.attribute())
		}
		if strings.HasPrefix(fixture.mode, "metaarray") {
			href := fixture.server.URL + "/api/remap/1.2/entity/customerorder/metadata/attributes"
			if fixture.mode == "metaarray-foreign" {
				href = "https://example.invalid/api/remap/1.2/entity/customerorder/metadata/attributes"
			}
			fixture.reply(writer, map[string]any{"attributes": map[string]any{"meta": map[string]any{"href": href, "type": "attributemetadata", "size": len(rows)}}})
			return
		}
		if fixture.mode == "null" {
			fixture.reply(writer, map[string]any{"attributes": nil})
			return
		}
		fixture.reply(writer, map[string]any{"attributes": rows})
		return
	}
	if request.Method == http.MethodGet && path == "entity/customerorder/metadata/attributes" {
		fixture.attributePages.Add(1)
		fixture.reply(writer, map[string]any{"meta": map[string]any{"size": 1, "limit": 100, "offset": 0}, "rows": []any{fixture.attribute()}})
		return
	}
	if request.Method != http.MethodPost || path != "entity/customerorder/metadata/attributes" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	fixture.writes.Add(1)
	var body []map[string]any
	if json.NewDecoder(request.Body).Decode(&body) == nil {
		fixture.captured <- body
	}
	if fixture.mode == "403" || fixture.mode == "500" {
		status := http.StatusForbidden
		if fixture.mode == "500" {
			status = http.StatusInternalServerError
		}
		writer.WriteHeader(status)
		fixture.reply(writer, map[string]any{"error": "private provider content"})
		return
	}
	fixture.created.Store(true)
	if fixture.mode == "lost" {
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err == nil {
			if err = connection.Close(); err != nil {
				return
			}
		}
		return
	}
	attribute := fixture.attribute()
	if fixture.mode == "wrong-type" {
		attribute["type"] = "text"
	}
	if fixture.mode == "foreign" {
		attribute["accountId"] = orderTestID(99)
	}
	fixture.reply(writer, []any{attribute})
}

func (*metadataCreateSimulator) reply(writer http.ResponseWriter, value any) {
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return
	}
}

func TestMetadataCreateOneExactNativeAttributeAndIndependentReadback(t *testing.T) {
	t.Parallel()
	fixture, client := newMetadataCreateSimulator(t, "ok")
	var authorizations atomic.Int32
	completion, err := client.CreateOrderAttribute(context.Background(), fixture.intent.Scope, fixture.intent, func(context.Context) (bool, error) { authorizations.Add(1); return true, nil })
	if err != nil || completion.State != orders.StateReconciled || completion.AttributeID != orderTestID(20) {
		t.Fatalf("completion=%+v err=%v", completion, err)
	}
	if fixture.writes.Load() != 1 || fixture.reads.Load() != 2 || authorizations.Load() != 1 {
		t.Fatal("one attempt or independent readback missing")
	}
	body := <-fixture.captured
	if len(body) != 1 || len(body[0]) != 3 || body[0]["name"] != fixture.intent.Marker || body[0]["type"] != "string" || body[0]["required"] != false {
		t.Fatalf("unsafe native body: %+v", body)
	}
	completion, err = client.CreateOrderAttribute(context.Background(), fixture.intent.Scope, fixture.intent, nil)
	if err != nil || completion.State != orders.StateReconciled || fixture.writes.Load() != 1 {
		t.Fatal("recovered marker caused another write")
	}
}

func TestMetadataCreateUnknownNeverResendsAndCanRecoverReadOnly(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"lost", "lag"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture, client := newMetadataCreateSimulator(t, mode)
			completion, err := client.CreateOrderAttribute(context.Background(), fixture.intent.Scope, fixture.intent, func(context.Context) (bool, error) { return true, nil })
			if !errors.Is(err, ErrOrderUnknown) || completion.State != orders.StateUnknown || fixture.writes.Load() != 1 {
				t.Fatalf("completion=%+v err=%v", completion, err)
			}
			fixture.visible.Store(true)
			intent := fixture.intent
			intent.State, intent.DispatchPermitted = orders.StateUnknown, false
			completion, err = client.CreateOrderAttribute(context.Background(), intent.Scope, intent, nil)
			if err != nil || completion.State != orders.StateReconciled || completion.ExpectedState != orders.StateUnknown || fixture.writes.Load() != 1 {
				t.Fatalf("recovery=%+v err=%v", completion, err)
			}
		})
	}
}

func TestMetadataCreateRejectionsAndAmbiguousAcknowledgements(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		mode  string
		state orders.State
	}{{"403", orders.StateRejected}, {"500", orders.StateUnknown}, {"wrong-type", orders.StateUnknown}, {"foreign", orders.StateUnknown}} {
		t.Run(test.mode, func(t *testing.T) {
			t.Parallel()
			fixture, client := newMetadataCreateSimulator(t, test.mode)
			completion, err := client.CreateOrderAttribute(context.Background(), fixture.intent.Scope, fixture.intent, func(context.Context) (bool, error) { return true, nil })
			if err == nil || completion.State != test.state || fixture.writes.Load() != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("completion=%+v err=%v", completion, err)
			}
			if test.mode == "403" && !errors.Is(err, ErrOrderAttributeAdmin) {
				t.Fatal("provider administrator denial not classified")
			}
		})
	}
}

func TestMetadataRecoveryAbsenceAndDuplicateMarkersCannotWrite(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"ok", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture, client := newMetadataCreateSimulator(t, mode)
			intent := fixture.intent
			intent.DispatchPermitted = false
			completion, err := client.CreateOrderAttribute(context.Background(), intent.Scope, intent, func(context.Context) (bool, error) { t.Error("recovery called write authorizer"); return true, nil })
			if err == nil || completion.State != orders.StateUnknown || fixture.writes.Load() != 0 {
				t.Fatalf("completion=%+v err=%v", completion, err)
			}
		})
	}
}

func TestMetadataCreateFinalGuardAfterCoordinatedAdmission(t *testing.T) {
	t.Parallel()
	fixture, client := newMetadataCreateSimulator(t, "ok")
	store := &orderGuardStore{}
	gate, err := providercall.New(store, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.client.Coordinate(gate, providercall.Scope{Tenant: fixture.intent.Scope.Tenant, Provider: "moysklad", Account: fixture.intent.Scope.MoyskladAccount, CredentialRevision: 1}); err != nil {
		t.Fatal(err)
	}
	var afterAdmission atomic.Bool
	completion, err := client.CreateOrderAttribute(context.Background(), fixture.intent.Scope, fixture.intent, func(context.Context) (bool, error) { afterAdmission.Store(store.started.Load()); return false, nil })
	if !errors.Is(err, ErrOrderIntent) || completion.State != orders.StateRejected || !afterAdmission.Load() || fixture.writes.Load() != 0 {
		t.Fatalf("completion=%+v err=%v", completion, err)
	}
}

func TestMetadataMarkerReaderAcceptsStoredHexAndUUIDOnly(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		marker string
		valid  bool
	}{{"styk-order-attribute-0123456789abcdef0123456789abcdef", true}, {"styk-order-attribute-" + orderTestID(1), true}, {"styk-order-attribute-unsafe", false}, {"prefix-styk-order-attribute-" + orderTestID(1), false}} {
		if validOrderAttributeMarker(test.marker) != test.valid {
			t.Fatalf("marker validity=%v", test.valid)
		}
	}
}

func TestMetadataCreateReadsNativeMetaArrayAndFollowsScopedCollection(t *testing.T) {
	t.Parallel()
	fixture, client := newMetadataCreateSimulator(t, "metaarray")
	completion, err := client.CreateOrderAttribute(context.Background(), fixture.intent.Scope, fixture.intent, func(context.Context) (bool, error) { return true, nil })
	if err != nil || completion.State != orders.StateReconciled || fixture.writes.Load() != 1 || fixture.attributePages.Load() != 1 {
		t.Fatalf("completion=%+v err=%v pages=%d", completion, err, fixture.attributePages.Load())
	}
}

func TestMetadataCreateRejectsNullAndForeignMetaArrayBeforeWrite(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"null", "metaarray-foreign"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture, client := newMetadataCreateSimulator(t, mode)
			completion, err := client.CreateOrderAttribute(context.Background(), fixture.intent.Scope, fixture.intent, func(context.Context) (bool, error) {
				t.Error("invalid metadata reached dispatch authorization")
				return true, nil
			})
			if err == nil || completion.State != orders.StateUnknown || fixture.writes.Load() != 0 {
				t.Fatalf("completion=%+v err=%v", completion, err)
			}
		})
	}
}

func TestMetadataSetupDoesNotEnableUnsafeExistingAttributeReplacement(t *testing.T) {
	t.Parallel()
	fixture, client := newOrderSimulator(t, "ok")
	command := fixture.command
	command.Action, command.ExpectedTargetRevision = orders.ActionMirrorUpdate, "native-revision"
	command.Correlation.CustomerOrderID = orderTestID(9)
	command.CustomerOrder.OwnedFields = []string{"attribute"}
	command.CustomerOrder.Attributes = []orders.AttributeWrite{{AttributeID: orderTestID(20), Value: orders.Present("cancelled")}}
	if err := client.BindOrderIntent(command, func(context.Context) (bool, error) { return true, nil }); !errors.Is(err, orders.ErrBlocked) || fixture.writes.Load() != 0 {
		t.Fatal("setup enabled unverified replacement of existing native attributes")
	}
}
