package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

func TestOrderCreateUsesNativeReferencesAndExactMinorUnits(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	command := simulator.command
	command.PII = &orders.Customer{Name: orders.Present("private buyer"), Phone: orders.Present("private phone")}
	var authorized atomic.Int32
	if err := client.BindOrderIntent(command, func(context.Context) (bool, error) { authorized.Add(1); return true, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteOrder(context.Background(), command)
	if err != nil || receipt.State != orders.StateAcknowledged || receipt.ExternalID != orderTestID(9) {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if simulator.writes.Load() != 1 || authorized.Load() != 1 {
		t.Fatal("expected one authorized write")
	}
	body := <-simulator.captured
	position := body["positions"].([]any)[0].(map[string]any)
	if position["price"].(json.Number) != "9007199254740991.5" {
		t.Fatalf("inexact minor price: %v", position["price"])
	}
	if position["assortment"].(map[string]any)["meta"].(map[string]any)["type"] != "variant" {
		t.Fatal("variant reference was lost")
	}
	if body["syncId"] != command.Correlation.SyncID || body["externalCode"] != command.Correlation.Marker {
		t.Fatal("scoped correlation marker missing")
	}
	if body["applicable"] != false || position["reserve"].(json.Number) != "0" {
		t.Fatal("explicit false or zero was dropped")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"payedSum", "shippedSum", "\"sum\"", "payments", "demands", "private", "ozon-sku", "seller-offer"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("forbidden payload data %q", forbidden)
		}
	}
	if _, err = client.WriteOrder(context.Background(), command); !errors.Is(err, ErrOrderIntent) || simulator.writes.Load() != 1 {
		t.Fatal("intent was reused")
	}
}

func TestOrderLostResponseIsUnknownAndCannotBeResent(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "lost")
	command := simulator.command
	if err := client.BindOrderIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteOrder(context.Background(), command)
	if !errors.Is(err, ErrOrderUnknown) || receipt.State != orders.StateUnknown || simulator.writes.Load() != 1 {
		t.Fatalf("receipt=%+v err=%v writes=%d", receipt, err, simulator.writes.Load())
	}
	if _, err = client.WriteOrder(context.Background(), command); !errors.Is(err, ErrOrderIntent) || simulator.writes.Load() != 1 {
		t.Fatal("uncertain write was resent")
	}
	correlation, err := client.ReadOrderCorrelation(context.Background(), command.Correlation)
	if err != nil || correlation.CustomerOrderID != orderTestID(9) || simulator.writes.Load() != 1 {
		t.Fatalf("correlation=%+v err=%v", correlation, err)
	}
}

func TestOrderProviderResponseClassification(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		mode  string
		state orders.State
	}{{"400", orders.StateRejected}, {"429", orders.StateRejected}, {"500", orders.StateUnknown}, {"302", orders.StateUnknown}, {"malformed", orders.StateUnknown}} {
		t.Run(test.mode, func(t *testing.T) {
			t.Parallel()
			simulator, client := newOrderSimulator(t, test.mode)
			if err := client.BindOrderIntent(simulator.command, func(context.Context) (bool, error) { return true, nil }); err != nil {
				t.Fatal(err)
			}
			receipt, err := client.WriteOrder(context.Background(), simulator.command)
			if receipt.State != test.state || simulator.writes.Load() != 1 {
				t.Fatalf("receipt=%+v err=%v writes=%d", receipt, err, simulator.writes.Load())
			}
			if strings.Contains(receipt.Code, "private") || (err != nil && strings.Contains(err.Error(), "private")) {
				t.Fatal("raw provider error escaped")
			}
		})
	}
}

func TestOrderReadbackFetchesEveryPositionAndPreservesPresence(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	correlation := simulator.command.Correlation
	correlation.CustomerOrderID = orderTestID(9)
	target, err := client.ReadCustomerOrder(context.Background(), correlation)
	if err != nil || !target.Complete || len(target.Positions) != 2 || simulator.positionPages.Load() != 2 {
		t.Fatalf("target=%+v err=%v pages=%d", target, err, simulator.positionPages.Load())
	}
	if target.Fields.CurrencyID != orderTestID(7) || target.Fields.AgentID != orderTestID(3) || target.Fields.SalesChannelID != orderTestID(6) {
		t.Fatal("native references incomplete")
	}
	if target.Fields.Applicable.Presence != orders.PresenceZero || target.Positions[0].Reserve.Presence != orders.PresenceZero || target.Fields.Description.Presence != orders.PresenceMissing {
		t.Fatal("provider presence lost")
	}
	if target.Positions[0].Price.Value.Amount != "90071992547409.915" || target.Sum.Value.Amount != "180143985094819.83" {
		t.Fatal("readback money is inexact")
	}
	for _, position := range target.Positions {
		if position.Managed || position.Identity.SKU != "" || position.Identity.OfferID != "" {
			t.Fatal("source line ownership was guessed")
		}
	}
	raw, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private") {
		t.Fatal("unmanaged PII escaped readback")
	}
}

func TestOrderReadbackRejectsAccountMismatchAndConcurrentChanges(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		drift, foreign bool
		expected       error
	}{{"changed", true, false, orders.ErrStale}, {"foreign", false, true, ErrOrderIdentity}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			simulator, client := newOrderSimulator(t, "ok")
			simulator.drift, simulator.wrongAccount = test.drift, test.foreign
			correlation := simulator.command.Correlation
			correlation.CustomerOrderID = orderTestID(9)
			target, err := client.ReadCustomerOrder(context.Background(), correlation)
			if !errors.Is(err, test.expected) || target.Complete {
				t.Fatalf("complete=%v err=%v", target.Complete, err)
			}
		})
	}
}

func TestOrderBindingRejectsScopeAndTransientPIIMutation(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	command := simulator.command
	command.PII = &orders.Customer{Name: orders.Present("buyer one")}
	if err := client.BindOrderIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	command.PII.Name = orders.Present("buyer two")
	if _, err := client.WriteOrder(context.Background(), command); !errors.Is(err, ErrOrderIntent) {
		t.Fatal("transient PII escaped intent binding")
	}
	command = simulator.command
	command.Scope.Tenant = "other-tenant"
	if _, err := client.WriteOrder(context.Background(), command); !errors.Is(err, ErrOrderIntent) || simulator.writes.Load() != 0 {
		t.Fatal("cross-tenant write admitted")
	}
}

func TestOrderCorrelationAbsenceAndRecoveredCreateNeverResend(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	_, err := client.ReadOrderCorrelation(context.Background(), simulator.command.Correlation)
	if !errors.Is(err, ErrOrderCorrelation) || !errors.Is(err, orders.ErrNotFound) {
		t.Fatalf("absence=%v", err)
	}
	simulator.created.Store(true)
	if err = client.BindOrderIntent(simulator.command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = client.WriteOrder(context.Background(), simulator.command); !errors.Is(err, orders.ErrConflict) || simulator.writes.Load() != 0 {
		t.Fatal("existing correlation was recreated")
	}
}

type orderGuardStore struct{ started atomic.Bool }

func (store *orderGuardStore) TryAcquireProviderCall(_ context.Context, attempt providercall.Attempt) (providercall.Admission, error) {
	return providercall.Admission{Lease: providercall.Lease{Attempt: attempt}}, nil
}
func (store *orderGuardStore) StartProviderCall(_ context.Context, lease providercall.Lease) (time.Duration, error) {
	if lease.Attempt.IsWrite {
		store.started.Store(true)
	}
	return 0, nil
}
func (*orderGuardStore) FinishProviderCall(context.Context, providercall.Lease, providercall.Result) error {
	return nil
}

func TestOrderFinalGuardRunsInsideCoordinatedTransport(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	store := &orderGuardStore{}
	gate, err := providercall.New(store, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.client.Coordinate(gate, providercall.Scope{Tenant: client.scope.Tenant, Provider: "moysklad", Account: client.scope.MoyskladAccount, CredentialRevision: 1}); err != nil {
		t.Fatal(err)
	}
	var called atomic.Int32
	var afterAdmission atomic.Bool
	if err = client.BindOrderIntent(simulator.command, func(context.Context) (bool, error) {
		called.Add(1)
		afterAdmission.Store(store.started.Load())
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteOrder(context.Background(), simulator.command)
	if !errors.Is(err, ErrOrderIntent) || receipt.State != orders.StateRejected || called.Load() != 1 || !afterAdmission.Load() || simulator.writes.Load() != 0 {
		t.Fatalf("receipt=%+v err=%v called=%d writes=%d", receipt, err, called.Load(), simulator.writes.Load())
	}
}

func TestOrderUpdatesOnlyExplicitOwnedFields(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	command := simulator.command
	command.Action = orders.ActionMirrorUpdate
	command.Correlation.CustomerOrderID = orderTestID(9)
	command.ExpectedTargetRevision = "expected-revision"
	command.CustomerOrder.OwnedFields = []string{"state"}
	body, err := client.orderPayload(command)
	if err != nil || len(body) != 1 || body["state"] == nil {
		t.Fatalf("body=%+v err=%v", body, err)
	}
	command.CustomerOrder.OwnedFields = []string{"positions.quantity"}
	if _, err = client.orderPayload(command); !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("unverified collection replacement admitted")
	}
	command.CustomerOrder.OwnedFields = []string{"organization"}
	if _, err = client.orderPayload(command); err == nil {
		t.Fatal("organization update omitted required account reference")
	}
}

func TestOrderWriteHTTPRejectsRedirectAndReplay(t *testing.T) {
	t.Parallel()
	client, closeHTTP := orderHTTP(&http.Client{})
	defer closeHTTP()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || !transport.DisableKeepAlives || transport.Protocols.HTTP2() || client.Timeout != 15*time.Second {
		t.Fatal("write transport permits replay or unbounded response")
	}
	if !errors.Is(client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("redirects are enabled")
	}
}

func TestOrderReadbackOmitsUnmanagedTextAttributes(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	managed := simulator.reference("entity/customerorder/metadata/attributes", "attributemetadata", 12)
	managed["id"], managed["type"], managed["name"], managed["value"] = orderTestID(12), "string", "styk-order-attribute-"+orderTestID(13), "posting-1"
	unmanaged := simulator.reference("entity/customerorder/metadata/attributes", "attributemetadata", 14)
	unmanaged["id"], unmanaged["type"], unmanaged["name"], unmanaged["value"] = orderTestID(14), "text", "Customer phone", "private phone"
	raw, err := json.Marshal([]any{managed, unmanaged})
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := client.decodeOrderAttributes(raw)
	if err != nil || len(attributes) != 1 || attributes[0].AttributeID != orderTestID(12) {
		t.Fatalf("attributes=%+v err=%v", attributes, err)
	}
}

func TestOrderJSONRejectsDuplicateAndDeepKeys(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{"accountId":"one","accountId":"two"}`, strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66), `{"nested":{"value":1,"value":2}}`} {
		if err := orderCheckJSON([]byte(raw)); !errors.Is(err, ErrOrderSchema) {
			t.Fatal("ambiguous JSON admitted")
		}
	}
}

func TestAcknowledgedOrderReceiptUsesDurableSafeCode(t *testing.T) {
	command := orderTestCommand(t)
	receipt := orderReceipt(command, orders.StateAcknowledged, 200, "provider_acknowledged")
	if receipt.Code != "PROVIDER_ACKNOWLEDGED" || receipt.State != orders.StateAcknowledged || receipt.HTTPStatus != 200 {
		t.Fatal("acknowledgement was mislabeled as a provider error")
	}
}
