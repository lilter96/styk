package moyskladfixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeTransport struct {
	items         map[string]map[string]any
	posts         int
	loseResponse  bool
	wrongAccount  bool
	activeWebhook bool
	mismatch      bool
	methods       []string
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.methods = append(f.methods, r.Method)
	if r.URL.Host != "api.moysklad.ru" || r.Header.Get("Authorization") != "Bearer fixture-only" {
		return nil, errors.New("unexpected request")
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/remap/1.2/")
	account := testAccount
	if f.wrongAccount {
		account = "10000000-0000-0000-0000-000000000001"
	}
	var body any
	switch {
	case r.Method == http.MethodPost && path == "entity/store":
		var v map[string]any
		if json.NewDecoder(r.Body).Decode(&v) != nil {
			return nil, errors.New("invalid JSON")
		}
		f.posts++
		id := fmt.Sprintf("20000000-0000-0000-0000-%012d", f.posts)
		v["id"] = id
		v["accountId"] = account
		f.items[id] = v
		if f.loseResponse {
			f.loseResponse = false
			return nil, io.ErrUnexpectedEOF
		}
		body = v
	case r.Method == http.MethodGet && strings.HasPrefix(path, "entity/store/"):
		item := f.items[strings.TrimPrefix(path, "entity/store/")]
		if f.mismatch {
			copied := map[string]any{}
			for k, v := range item {
				copied[k] = v
			}
			copied["name"] = "unexpected"
			body = copied
		} else {
			body = item
		}
	case r.Method == http.MethodGet && path == "entity/variant/metadata":
		body = map[string]any{"meta": map[string]any{}}
	case r.Method == http.MethodGet && strings.HasPrefix(path, "entity/"):
		kind := strings.TrimPrefix(path, "entity/")
		rows := []map[string]any{}
		if kind == "organization" {
			rows = append(rows, map[string]any{"id": "30000000-0000-0000-0000-000000000001", "accountId": account})
		}
		if kind == "store" {
			filter := strings.Split(r.URL.Query().Get("filter"), ";")[0]
			if filter == "" {
				rows = append(rows, map[string]any{"id": "30000000-0000-0000-0000-000000000002", "accountId": account})
			} else {
				for _, v := range f.items {
					if "code="+str(v, "code") == filter || (strings.HasPrefix(filter, "code~=") && strings.HasPrefix(str(v, "code"), strings.TrimPrefix(filter, "code~="))) {
						rows = append(rows, v)
					}
				}
			}
		}
		if kind == "webhook" && f.activeWebhook {
			rows = append(rows, map[string]any{"accountId": account, "enabled": true})
		}
		body = map[string]any{"meta": map[string]any{"size": len(rows), "limit": 1000, "offset": 0}, "rows": rows}
	default:
		return nil, errors.New("unexpected operation")
	}
	b, _ := json.Marshal(body)
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: r}, nil
}

func TestClientRejectsDuplicateNamespaceBeforeAnyCreation(t *testing.T) {
	l, m, _ := fixture(t)
	f := &fakeTransport{items: map[string]map[string]any{"30000000-0000-0000-0000-000000000003": {"id": "30000000-0000-0000-0000-000000000003", "accountId": testAccount, "code": testNamespace + "-unrelated-synthetic-key"}}}
	c := mockClient(t, f)
	if err := Run(context.Background(), l, &m, testAccount, c, 1); !errors.Is(err, ErrDuplicate) || f.posts != 0 || m.NamespaceChecked {
		t.Fatal("duplicate namespace reached creation", err)
	}
}

func mockClient(t *testing.T, transport *fakeTransport) *Client {
	t.Helper()
	c, err := NewClient(testAccount, "fixture-only", time.Second, 400*time.Millisecond, 200, transport)
	if err != nil {
		t.Fatal(err)
	}
	c.interval = 0
	return c
}

func TestClientRejectsMissingCredentialsAndWriteOutsideAllowlist(t *testing.T) {
	if _, err := NewClient(testAccount, "", time.Second, time.Second, 10, nil); err == nil {
		t.Fatal("missing credentials accepted")
	}
	f := &fakeTransport{items: map[string]map[string]any{}}
	c := mockClient(t, f)
	for _, tc := range []struct{ method, path string }{{"DELETE", "entity/store"}, {"PUT", "entity/product"}, {"POST", "entity/store/delete"}, {"POST", "entity/paymentin"}, {"POST", "entity/store/20000000-0000-0000-0000-000000000001"}} {
		if err := c.call(context.Background(), tc.method, tc.path, nil, nil, new(any)); err == nil {
			t.Fatal("unapproved endpoint accepted")
		}
	}
	if len(f.methods) != 0 {
		t.Fatal("rejected operation reached transport")
	}
}

func TestClientBlocksWrongAccountAndEnabledWebhooks(t *testing.T) {
	for _, tc := range []struct {
		name           string
		wrong, webhook bool
	}{{"wrong account", true, false}, {"active webhook", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			l, m, _ := fixture(t)
			f := &fakeTransport{items: map[string]map[string]any{}, wrongAccount: tc.wrong, activeWebhook: tc.webhook}
			c := mockClient(t, f)
			if err := Run(context.Background(), l, &m, testAccount, c, 1); err == nil || f.posts != 0 {
				t.Fatal("unsafe preflight accepted")
			}
		})
	}
}

func TestClientLostResponseResumesByUniqueIdentity(t *testing.T) {
	l, m, _ := fixture(t)
	f := &fakeTransport{items: map[string]map[string]any{}, loseResponse: true}
	c := mockClient(t, f)
	if err := Run(context.Background(), l, &m, testAccount, c, 1); !errors.Is(err, ErrUnknownOutcome) || f.posts != 1 {
		t.Fatal("lost response not marked unknown")
	}
	persisted, err := l.Load()
	if err != nil || persisted.Operations[0].State != Unknown {
		t.Fatal("unknown state not durable")
	}
	if err = Run(context.Background(), l, &persisted, testAccount, c, 1); !errors.Is(err, ErrOperationLimit) || f.posts != 2 || persisted.Operations[0].State != Created {
		t.Fatal("unsafe reconciliation or incorrect resume", err)
	}
	for _, method := range f.methods {
		if method != "GET" && method != "POST" {
			t.Fatal("unexpected external method")
		}
	}
}

func TestClientCancellationAndNumericValues(t *testing.T) {
	f := &fakeTransport{items: map[string]map[string]any{}}
	c := mockClient(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.call(ctx, "GET", "entity/store", nil, nil, new(any)); !errors.Is(err, context.Canceled) || len(f.methods) != 0 {
		t.Fatal("cancelled request reached transport")
	}
	if !equalNumber(json.Number("10.00"), 10) || equalNumber(nil, 0) || equalNumber(json.Number("0.1"), 1) {
		t.Fatal("numeric evidence comparison invalid")
	}
}

func TestClientPersistsIdentityOnRemoteValidationMismatch(t *testing.T) {
	l, m, _ := fixture(t)
	f := &fakeTransport{items: map[string]map[string]any{}, mismatch: true}
	c := mockClient(t, f)
	if err := Run(context.Background(), l, &m, testAccount, c, 1); !errors.Is(err, ErrUnknownOutcome) {
		t.Fatal("mismatch did not stop safely", err)
	}
	stored, err := l.Load()
	if err != nil || stored.Operations[0].State != Unknown || stored.Operations[0].RemoteID == "" || f.posts != 1 {
		t.Fatal("created identity was not durably preserved")
	}
	if err = Run(context.Background(), l, &stored, testAccount, c, 1); !errors.Is(err, ErrUnknownOutcome) || f.posts != 1 {
		t.Fatal("mismatch was retried")
	}
}

func TestFixturePayloadsAreNamespacedAndDocumentPositionsAreVerified(t *testing.T) {
	_, m, _ := fixture(t)
	for i := range m.Operations {
		m.Operations[i].State = Created
		m.Operations[i].RemoteID = fmt.Sprintf("20000000-0000-0000-0000-%012d", i+1)
	}
	c := mockClient(t, &fakeTransport{items: map[string]map[string]any{}})
	c.organization = "30000000-0000-0000-0000-000000000001"
	byKey := func(key string) Operation {
		for _, o := range m.Operations {
			if o.Key == key {
				return o
			}
		}
		t.Fatal("missing fixture operation")
		return Operation{}
	}
	d1, _ := c.payload(byKey("duplicate-a"), m)
	d2, _ := c.payload(byKey("duplicate-b"), m)
	if d1["article"] != d2["article"] || d1["externalCode"] == d2["externalCode"] {
		t.Fatal("duplicate articles or external IDs were collapsed")
	}
	missing, _ := c.payload(byKey("no-article"), m)
	if _, ok := missing["article"]; ok {
		t.Fatal("absent article invented")
	}
	o := byKey("order-reserved")
	expected, err := c.payload(o, m)
	if err != nil {
		t.Fatal(err)
	}
	remoteDoc := map[string]any{}
	for k, v := range expected {
		remoteDoc[k] = v
	}
	remoteDoc["id"] = o.RemoteID
	remoteDoc["accountId"] = testAccount
	pos := expected["positions"].([]any)[0].(map[string]any)
	pos["accountId"] = testAccount
	missingReserve := false
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var v any
		if strings.HasSuffix(r.URL.Path, "/positions") {
			if missingReserve {
				delete(pos, "reserve")
			}
			v = map[string]any{"meta": map[string]any{"size": 1, "limit": 100, "offset": 0}, "rows": []any{pos}}
		} else {
			v = remoteDoc
		}
		b, _ := json.Marshal(v)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: r}, nil
	})
	if err = c.verify(context.Background(), o, o.RemoteID, m); err != nil {
		t.Fatal("valid fixture rejected", err)
	}
	missingReserve = true
	if err = c.verify(context.Background(), o, o.RemoteID, m); err == nil {
		t.Fatal("missing reserve treated as zero or verified")
	}
}

func TestStockObservationDoesNotInventMissingRowsOrValues(t *testing.T) {
	_, m, _ := fixture(t)
	m.NamespaceChecked = true
	for i := range m.Operations {
		m.Operations[i].State = Created
		m.Operations[i].RemoteID = fmt.Sprintf("20000000-0000-0000-0000-%012d", i+1)
	}
	id, _ := findID(m, "product-a")
	warehouse, _ := findID(m, "warehouse-a")
	row := reference("product", id)
	// Live stock-report item references can carry a query; it is not identity.
	row["meta"].(map[string]any)["href"] = apiBase + "entity/product/" + id + "?expand=synthetic"
	store := reference("store", warehouse)
	store["stock"] = 10
	store["reserve"] = 2
	store["inTransit"] = 0
	row["stockByStore"] = []any{store}
	c := mockClient(t, &fakeTransport{items: map[string]map[string]any{}})
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/api/remap/1.2/report/stock/bystore" {
			t.Error("unexpected stock request")
		}
		v := map[string]any{"meta": map[string]any{"size": 1, "limit": 100, "offset": 0}, "rows": []any{row}}
		b, _ := json.Marshal(v)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: r}, nil
	})
	result, err := c.ObserveStock(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if result["product-a/warehouse-a"].Status != "STOCK_MATCH" || result["product-a/warehouse-a"].Reserve == nil || *result["product-a/warehouse-a"].Reserve != "2" {
		t.Fatal("reported quantities not retained")
	}
	if result["product-a/warehouse-b"].Status != "MISSING" || result["product-a/warehouse-b"].Stock != nil {
		t.Fatal("missing stock became zero")
	}
	if AllStockMatches(result) {
		t.Fatal("incomplete stock observation reported as fully matched")
	}
	store["reserve"] = "untrusted-source-string"
	result, err = c.ObserveStock(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if result["product-a/warehouse-a"].Reserve != nil || result["product-a/warehouse-a"].Status != "MISSING_VALUE" {
		t.Fatal("invalid reserve became evidence")
	}
}

func TestMissingCharacteristicCollectionDoesNotCreateVariants(t *testing.T) {
	_, m, _ := fixture(t)
	f := &fakeTransport{items: map[string]map[string]any{}}
	c := mockClient(t, f)
	if _, err := c.preflight(context.Background(), testAccount); err != nil {
		t.Fatal(err)
	}
	for _, o := range m.Operations {
		if o.Kind == "variant" || o.Kind == "characteristic" {
			if err := c.prepare(context.Background(), o, m); !errors.Is(err, ErrUnsupported) {
				t.Fatal("missing metadata accepted")
			}
		}
	}
	if f.posts != 0 {
		t.Fatal("missing metadata caused a write")
	}
}

func TestPartialShipmentRequiresIndependentOrderEvidence(t *testing.T) {
	_, m, _ := fixture(t)
	m.NamespaceChecked = true
	for i := range m.Operations {
		m.Operations[i].State = Created
		m.Operations[i].RemoteID = fmt.Sprintf("20000000-0000-0000-0000-%012d", i+1)
	}
	var shipment Operation
	for _, op := range m.Operations {
		if op.Key == "shipment-partial" {
			shipment = op
		}
	}
	c := mockClient(t, &fakeTransport{items: map[string]map[string]any{}})
	c.organization = "30000000-0000-0000-0000-000000000001"
	expected, err := c.payload(shipment, m)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	for k, v := range expected {
		doc[k] = v
	}
	doc["id"] = shipment.RemoteID
	doc["accountId"] = testAccount
	position := expected["positions"].([]any)[0].(map[string]any)
	position["accountId"] = testAccount
	product, _ := findID(m, "product-b")
	orderPosition := map[string]any{"accountId": testAccount, "assortment": reference("product", product), "quantity": 2, "shipped": 1}
	shippedMissing := false
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body any
		switch {
		case strings.Contains(r.URL.Path, "/entity/demand/") && strings.HasSuffix(r.URL.Path, "/positions"):
			body = map[string]any{"meta": map[string]any{"size": 1, "limit": 100, "offset": 0}, "rows": []any{position}}
		case strings.Contains(r.URL.Path, "/entity/customerorder/") && strings.HasSuffix(r.URL.Path, "/positions"):
			if shippedMissing {
				delete(orderPosition, "shipped")
			}
			body = map[string]any{"meta": map[string]any{"size": 1, "limit": 100, "offset": 0}, "rows": []any{orderPosition}}
		default:
			body = doc
		}
		b, _ := json.Marshal(body)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: r}, nil
	})
	if err = c.verify(context.Background(), shipment, shipment.RemoteID, m); err != nil {
		t.Fatal("valid shipment rejected", err)
	}
	shippedMissing = true
	if err = c.verify(context.Background(), shipment, shipment.RemoteID, m); err == nil {
		t.Fatal("missing shipped quantity accepted")
	}
}
