package moysklad

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type orderSimulator struct {
	server        *httptest.Server
	command       orders.Command
	writes        atomic.Int32
	details       atomic.Int32
	positionPages atomic.Int32
	created       atomic.Bool
	mode          string
	drift         bool
	wrongAccount  bool
	captured      chan map[string]any
}

func orderTestID(number int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", number) }

func orderTestCommand(t *testing.T) orders.Command {
	t.Helper()
	scope := orders.Scope{Tenant: "tenant", OzonAccount: "ozon-account", MoyskladAccount: orderTestID(1)}
	correlation, err := orders.CorrelationFor(scope, "posting-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	identity := orders.LineIdentity{ProviderLineID: "line-1", SKU: "ozon-sku", OfferID: "seller-offer"}
	mapping := orders.ProductMappingRef{ID: "mapping", Version: 1, Scope: scope, OzonProductID: "ozon-product", SKU: identity.SKU, OfferID: identity.OfferID, MoyskladKind: "variant", MoyskladID: orderTestID(8), Confirmed: true, ConfirmedAt: now}
	write := &orders.CustomerOrderWrite{OrganizationID: orderTestID(2), AgentID: orderTestID(3), StoreID: orderTestID(4), StateID: orderTestID(5), SalesChannelID: orderTestID(6), CurrencyID: orderTestID(7), Name: orders.Present("order-name"), Applicable: orders.Present(false), VATEnabled: orders.Present(false), VATIncluded: orders.Present(false), Moment: orders.Present(now)}
	write.Positions = []orders.PositionWrite{{Identity: identity, Mapping: mapping, Quantity: "2", Reserve: orders.Present(orders.Decimal("0")), Price: orders.Present(orders.Money{Amount: "90071992547409.915", Currency: "RUB"}), VAT: orders.Present(orders.Decimal("0"))}}
	return orders.Command{ID: "command-1", Scope: scope, Action: orders.ActionMirrorCreate, CommercialOrderID: "commercial-order", PostingNumber: correlation.PostingNumber, Correlation: correlation, ConfigRevision: 1, Credentials: orders.CredentialRevisions{Ozon: 1, Moysklad: 1}, ExpectedSourceRevision: "source-revision", SourceOrigin: orders.OriginOzon, Warehouse: orders.WarehouseMappingRef{ID: "warehouse-mapping", Version: 1, Scope: scope, OzonWarehouseID: "ozon-warehouse", MoyskladStoreID: orderTestID(4), Confirmed: true, ConfirmedAt: now}, CustomerOrder: write, IdempotencyKey: "intent-1", CreatedAt: now}
}

func newOrderSimulator(t *testing.T, mode string) (*orderSimulator, *OrderClient) {
	t.Helper()
	simulator := &orderSimulator{command: orderTestCommand(t), mode: mode, captured: make(chan map[string]any, 2)}
	simulator.server = httptest.NewServer(http.HandlerFunc(simulator.serveHTTP))
	t.Cleanup(simulator.server.Close)
	client, err := New(Options{BaseURL: simulator.server.URL + "/api/remap/1.2/", Token: "synthetic-token", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 3, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewOrderClient(client, simulator.command.Scope)
	if err != nil {
		t.Fatal(err)
	}
	return simulator, adapter
}

func (s *orderSimulator) reference(path, kind string, id int) map[string]any {
	return map[string]any{"meta": map[string]any{"href": s.server.URL + "/api/remap/1.2/" + path + "/" + orderTestID(id), "type": kind, "mediaType": "application/json"}}
}

func (s *orderSimulator) identity() map[string]any {
	row := s.reference("entity/customerorder", "customerorder", 9)
	row["id"], row["accountId"] = orderTestID(9), s.command.Scope.MoyskladAccount
	row["syncId"], row["externalCode"] = s.command.Correlation.SyncID, s.command.Correlation.Marker
	return row
}

func (s *orderSimulator) document() map[string]any {
	row := s.identity()
	row["updated"], row["name"], row["moment"] = "2026-10-30 11:00:00.000", "order-name", "2026-10-30 11:00:00.000"
	if s.drift && s.details.Load() > 1 {
		row["updated"] = "2026-10-30 11:01:00.000"
	}
	row["organization"], row["agent"] = s.reference("entity/organization", "organization", 2), s.reference("entity/counterparty", "counterparty", 3)
	row["store"], row["state"] = s.reference("entity/store", "store", 4), s.reference("entity/customerorder/metadata/states", "state", 5)
	row["salesChannel"] = s.reference("entity/saleschannel", "saleschannel", 6)
	row["rate"] = map[string]any{"currency": s.reference("entity/currency", "currency", 7), "value": json.Number("1")}
	row["applicable"], row["vatEnabled"], row["vatIncluded"] = false, false, false
	row["sum"] = json.Number("18014398509481983.0")
	row["positions"] = map[string]any{"meta": map[string]any{"href": s.server.URL + "/api/remap/1.2/entity/customerorder/" + orderTestID(9) + "/positions", "type": "customerorderposition", "size": 2, "limit": 1, "offset": 0}, "rows": []any{s.position(0)}}
	// Unknown customer fields are never exposed by the typed target projection.
	row["shipmentAddress"], row["unreviewed"] = "private address", map[string]any{"phone": "private phone"}
	return row
}

func (s *orderSimulator) position(index int) map[string]any {
	account := s.command.Scope.MoyskladAccount
	if s.wrongAccount {
		account = orderTestID(99)
	}
	return map[string]any{"id": orderTestID(10 + index), "accountId": account, "assortment": s.reference("entity/variant", "variant", 8), "quantity": json.Number("1"), "reserve": json.Number("0"), "price": json.Number("9007199254740991.5"), "vat": json.Number("0"), "discount": 0, "shipped": 0, "vatEnabled": false}
}

func (s *orderSimulator) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/remap/1.2/")
	writer.Header().Set("Content-Type", "application/json")
	if request.Method != http.MethodGet {
		s.write(writer, request)
		return
	}
	if path == "context/employee" {
		s.reply(writer, map[string]any{"accountId": s.command.Scope.MoyskladAccount})
		return
	}
	if path == "entity/customerorder/metadata" {
		s.metadata(writer)
		return
	}
	if path == "entity/customerorder" {
		rows := make([]any, 0)
		if s.created.Load() {
			rows = append(rows, s.identity())
		}
		s.replyPage(writer, rows, len(rows), 2, 0)
		return
	}
	if path == "entity/customerorder/"+orderTestID(9) {
		s.details.Add(1)
		s.reply(writer, s.document())
		return
	}
	if strings.HasSuffix(path, "/positions") {
		s.positionPages.Add(1)
		offset, _ := strconv.Atoi(request.URL.Query().Get("offset"))
		s.replyPage(writer, []any{s.position(offset)}, 2, 1, offset)
		return
	}
	s.dictionary(writer, path)
}

func (s *orderSimulator) dictionary(writer http.ResponseWriter, path string) {
	for index, kind := range []string{"organization", "counterparty", "store", "saleschannel"} {
		if path != "entity/"+kind {
			continue
		}
		id := []int{2, 3, 4, 6}[index]
		row := s.reference(path, kind, id)
		row["id"], row["accountId"], row["name"] = orderTestID(id), s.command.Scope.MoyskladAccount, kind
		s.replyPage(writer, []any{row}, 1, 1, 0)
		return
	}
	if path == "entity/currency" {
		row := s.reference(path, "currency", 7)
		row["id"], row["isoCode"], row["rate"], row["default"] = orderTestID(7), "RUB", json.Number("1"), true
		s.replyPage(writer, []any{row}, 1, 1, 0)
		return
	}
	writer.WriteHeader(http.StatusNotFound)
}

func (s *orderSimulator) metadata(writer http.ResponseWriter) {
	state := s.reference("entity/customerorder/metadata/states", "state", 5)
	state["id"], state["accountId"], state["entityType"], state["name"], state["stateType"] = orderTestID(5), s.command.Scope.MoyskladAccount, "customerorder", "state-name", "Regular"
	s.reply(writer, map[string]any{"states": []any{state}, "attributes": []any{}})
}

func (s *orderSimulator) write(writer http.ResponseWriter, request *http.Request) {
	s.writes.Add(1)
	decoder := json.NewDecoder(request.Body)
	decoder.UseNumber()
	var body map[string]any
	if decoder.Decode(&body) == nil {
		s.captured <- body
	}
	s.created.Store(true)
	if s.mode == "lost" {
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
		return
	}
	if status, err := strconv.Atoi(s.mode); err == nil {
		writer.WriteHeader(status)
		s.reply(writer, map[string]any{"error": "private provider message"})
		return
	}
	if s.mode == "malformed" {
		s.reply(writer, map[string]any{"accountId": "foreign"})
		return
	}
	s.reply(writer, s.identity())
}

func (s *orderSimulator) replyPage(writer http.ResponseWriter, rows []any, size, limit, offset int) {
	s.reply(writer, map[string]any{"meta": map[string]any{"size": size, "limit": limit, "offset": offset}, "rows": rows})
}

func (s *orderSimulator) reply(writer http.ResponseWriter, value any) {
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return
	}
}
