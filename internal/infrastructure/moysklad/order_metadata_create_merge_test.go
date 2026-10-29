package moysklad

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func newOrderAttributeUpdateSimulator(t *testing.T, kind string) (*orderSimulator, *OrderClient, orders.Command) {
	t.Helper()
	fixture := &orderSimulator{command: orderTestCommand(t), mode: "ok", captured: make(chan map[string]any, 2)}
	handler := func(writer http.ResponseWriter, request *http.Request) {
		path := strings.TrimPrefix(request.URL.Path, "/api/remap/1.2/")
		if request.Method == http.MethodGet && path == "entity/customerorder/metadata" {
			state := fixture.reference("entity/customerorder/metadata/states", "state", 5)
			state["id"], state["accountId"], state["entityType"], state["stateType"] = orderTestID(5), fixture.command.Scope.MoyskladAccount, "customerorder", "Regular"
			attribute := fixture.reference("entity/customerorder/metadata/attributes", "attributemetadata", 21)
			attribute["name"], attribute["type"] = "styk-order-attribute-"+orderTestID(21), "string"
			fixture.reply(writer, map[string]any{"states": []any{state}, "attributes": []any{attribute}})
			return
		}
		if request.Method == http.MethodGet && path == "entity/customerorder/"+orderTestID(9) {
			fixture.details.Add(1)
			document := fixture.document()
			attribute := fixture.reference("entity/customerorder/metadata/attributes", "attributemetadata", 20)
			attribute["id"], attribute["name"], attribute["type"], attribute["value"] = orderTestID(20), "styk-order-attribute-"+orderTestID(20), "string", "preserved operational value"
			if kind == "unowned" {
				attribute["name"] = "Customer phone"
			}
			if kind == "complex" {
				attribute["type"], attribute["value"] = "long", 7
			}
			document["attributes"] = []any{attribute}
			if kind == "empty" {
				document["attributes"] = []any{}
			}
			fixture.reply(writer, document)
			return
		}
		fixture.serveHTTP(writer, request)
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(fixture.server.Close)
	base, err := New(Options{BaseURL: fixture.server.URL + "/api/remap/1.2/", Token: "synthetic", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 3, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewOrderClient(base, fixture.command.Scope)
	if err != nil {
		t.Fatal(err)
	}
	command := fixture.command
	command.Action = orders.ActionMirrorUpdate
	command.Correlation.CustomerOrderID = orderTestID(9)
	observation, err := client.ReadCustomerOrder(context.Background(), command.Correlation)
	if err != nil {
		t.Fatal(err)
	}
	command.ExpectedTargetRevision = observation.Revision
	command.CustomerOrder.OwnedFields = []string{"attribute"}
	command.CustomerOrder.Attributes = []orders.AttributeWrite{{AttributeID: orderTestID(21), Value: orders.Present("cancelled")}}
	return fixture, client, command
}

func TestMetadataAttributeUpdatePreservesOmittedNativeSTYKValues(t *testing.T) {
	t.Parallel()
	fixture, client, command := newOrderAttributeUpdateSimulator(t, "owned")
	if err := client.BindOrderIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteOrder(context.Background(), command)
	if err != nil || receipt.State != orders.StateAcknowledged || fixture.writes.Load() != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	body := <-fixture.captured
	attributes := body["attributes"].([]any)
	if len(body) != 1 || len(attributes) != 2 {
		t.Fatalf("merged native body=%+v", body)
	}
	values := make(map[string]any)
	for _, value := range attributes {
		attribute := value.(map[string]any)
		href := attribute["meta"].(map[string]any)["href"].(string)
		values[href[strings.LastIndexByte(href, '/')+1:]] = attribute["value"]
	}
	if values[orderTestID(20)] != "preserved operational value" || values[orderTestID(21)] != "cancelled" {
		t.Fatal("omitted native attribute was erased")
	}
}

func TestMetadataAttributeUpdateBlocksUnownedComplexAndStaleSnapshots(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"unowned", "complex", "stale"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			fixture, client, command := newOrderAttributeUpdateSimulator(t, kind)
			if kind == "stale" {
				fixture.drift = true
			}
			if err := client.BindOrderIntent(command, func(context.Context) (bool, error) {
				t.Error("blocked attributes reached final dispatch")
				return true, nil
			}); err != nil {
				t.Fatal(err)
			}
			receipt, err := client.WriteOrder(context.Background(), command)
			if err == nil || receipt.State != orders.StateRejected || fixture.writes.Load() != 0 {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			if kind == "stale" && !errors.Is(err, orders.ErrStale) {
				t.Fatalf("stale snapshot=%v", err)
			}
		})
	}
}

func TestMetadataAttributeUpdateCanPopulateAnEmptyNativeCollection(t *testing.T) {
	t.Parallel()
	fixture, client, command := newOrderAttributeUpdateSimulator(t, "empty")
	if err := client.BindOrderIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteOrder(context.Background(), command)
	if err != nil || receipt.State != orders.StateAcknowledged || fixture.writes.Load() != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	body := <-fixture.captured
	attributes := body["attributes"].([]any)
	if len(body) != 1 || len(attributes) != 1 || attributes[0].(map[string]any)["value"] != "cancelled" {
		t.Fatalf("unexpected first native attribute payload=%+v", body)
	}
}
