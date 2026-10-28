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
)

func existingFixture(t *testing.T) (*Lock, Manifest, *fakeRemote) {
	t.Helper()
	l, m, remote := fixture(t)
	m.NamespaceChecked = true
	for i := range m.Operations {
		o := &m.Operations[i]
		switch o.Key {
		case "variant-characteristic", "variant-one", "variant-two":
			o.State = Skipped
			o.Note = "fixture prerequisite unavailable"
		default:
			o.State = Created
			o.RemoteID = fmt.Sprintf("20000000-0000-0000-0000-%012d", i+1)
			remote.items[o.Key] = o.RemoteID
		}
	}
	if err := l.Save(m); err != nil {
		t.Fatal(err)
	}
	return l, m, remote
}

func opByKey(t *testing.T, m Manifest, key string) Operation {
	t.Helper()
	for _, o := range m.Operations {
		if o.Key == key {
			return o
		}
	}
	t.Fatalf("missing fixture %s", key)
	return Operation{}
}

func jsonResponse(r *http.Request, value any) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: r}
}

func TestCharacteristicOptionalRowsAndNamespaceCollision(t *testing.T) {
	_, m, _ := existingFixture(t)
	characteristic := opByKey(t, m, "variant-characteristic")
	c := mockClient(t, &fakeTransport{items: map[string]map[string]any{}})
	includeCollision := false
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatal("unexpected write")
		}
		body := map[string]any{"meta": map[string]any{}}
		if includeCollision && strings.HasSuffix(r.URL.Path, "/characteristics") {
			body["rows"] = []any{map[string]any{"id": "40000000-0000-0000-0000-000000000001", "name": characteristic.Identity}}
		}
		return jsonResponse(r, body), nil
	})
	ids, err := c.search(context.Background(), characteristic)
	if len(ids) != 0 || !errors.Is(err, ErrCoverageUnknown) {
		t.Fatal("missing optional rows were treated as proof of absence")
	}
	includeCollision = true
	ids, err = c.search(context.Background(), characteristic)
	if len(ids) != 1 || !errors.Is(err, ErrCoverageUnknown) {
		t.Fatal("visible namespace collision was missed")
	}
}

func TestCharacteristicCreationAndIndependentIdentityVerification(t *testing.T) {
	l, m, _ := existingFixture(t)
	if err := EnableVariantContinuation(l, &m, testAccount); err != nil {
		t.Fatal(err)
	}
	o := opByKey(t, m, "variant-characteristic")
	id := "40000000-0000-0000-0000-000000000001"
	posts, gets := 0, 0
	mismatch := false
	c := mockClient(t, &fakeTransport{items: map[string]map[string]any{}})
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/metadata/characteristics") {
			posts++
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["name"] != o.Identity {
				t.Fatal("characteristic POST was not fixture-specific")
			}
			return jsonResponse(r, map[string]any{"id": id}), nil
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/metadata/characteristics/"+id) {
			gets++
			name := o.Identity
			if mismatch {
				name = "different characteristic"
			}
			return jsonResponse(r, map[string]any{"id": id, "name": name, "type": "string", "required": false, "meta": map[string]any{"href": apiBase + "entity/variant/metadata/characteristics/" + id, "type": "attributemetadata", "mediaType": "application/json"}}), nil
		}
		t.Fatal("unallowlisted operation")
		return nil, errors.New("unexpected request")
	})
	actual, err := c.create(context.Background(), o, m)
	if err != nil || actual != id || posts != 1 {
		t.Fatal("characteristic creation failed")
	}
	if err = c.verify(context.Background(), o, actual, m); err != nil || gets != 1 {
		t.Fatal("independent characteristic verification failed", err)
	}
	mismatch = true
	if err = c.verify(context.Background(), o, actual, m); err == nil {
		t.Fatal("incorrect characteristic name was accepted")
	}
}

func TestCharacteristicUnknownOutcomeNeverRetried(t *testing.T) {
	l, m, remote := existingFixture(t)
	if err := EnableVariantContinuation(l, &m, testAccount); err != nil {
		t.Fatal(err)
	}
	remote.loseResponse = true
	remote.coverageUnknown = true
	if err := Run(context.Background(), l, &m, testAccount, remote, 1); !errors.Is(err, ErrUnknownOutcome) || remote.posts != 1 {
		t.Fatal("lost characteristic response was not recorded as unknown", err)
	}
	persisted, err := l.Load()
	if err != nil || opByKey(t, persisted, "variant-characteristic").State != Unknown || opByKey(t, persisted, "variant-characteristic").RemoteID != "" {
		t.Fatal("unknown characteristic was not durable")
	}
	if err = Run(context.Background(), l, &persisted, testAccount, remote, 1); !errors.Is(err, ErrUnknownOutcome) || remote.posts != 1 {
		t.Fatal("unknown characteristic was retried")
	}
	if err = EnableVariantContinuation(l, &persisted, testAccount); err == nil {
		t.Fatal("unknown operation was replanned")
	}
}

func TestCharacteristicReturnedIDIsJournaledBeforeVerification(t *testing.T) {
	l, m, remote := existingFixture(t)
	if err := EnableVariantContinuation(l, &m, testAccount); err != nil {
		t.Fatal(err)
	}
	remote.verificationFailKey = "variant-characteristic"
	remote.coverageUnknown = true
	if err := Run(context.Background(), l, &m, testAccount, remote, 1); !errors.Is(err, ErrUnknownOutcome) || remote.posts != 1 {
		t.Fatal("failed independent verification did not stop safely")
	}
	persisted, err := l.Load()
	o := opByKey(t, persisted, "variant-characteristic")
	if err != nil || o.State != Unknown || o.RemoteID == "" || o.RemoteID != remote.items[o.Key] {
		t.Fatal("returned characteristic ID was not saved before verification")
	}
	remote.verificationFailKey = ""
	if err = Run(context.Background(), l, &persisted, testAccount, remote, 1); !errors.Is(err, ErrOperationLimit) || remote.posts != 2 || opByKey(t, persisted, o.Key).State != Created {
		t.Fatal("resume did not verify the saved ID before continuing without duplication", err)
	}
}

func TestCharacteristicVisibleNamespaceCollisionStopsCreation(t *testing.T) {
	l, m, remote := existingFixture(t)
	if err := EnableVariantContinuation(l, &m, testAccount); err != nil {
		t.Fatal(err)
	}
	remote.coverageUnknown = true
	remote.items["variant-characteristic"] = "60000000-0000-0000-0000-000000000001"
	if err := Run(context.Background(), l, &m, testAccount, remote, 1); !errors.Is(err, ErrDuplicate) || remote.posts != 0 {
		t.Fatal("visible characteristic collision did not stop creation", err)
	}
}

func TestVariantContinuationResumesWithoutChangingExistingFixtures(t *testing.T) {
	l, m, remote := existingFixture(t)
	if err := EnableVariantContinuation(l, &m, testAccount); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), l, &m, testAccount, remote, 5); err != nil || remote.posts != 5 {
		t.Fatal("continuation did not create exactly five new entities", err)
	}
	if err := Run(context.Background(), l, &m, testAccount, remote, 5); err != nil || remote.posts != 5 {
		t.Fatal("repeated continuation duplicated an entity", err)
	}
	if opByKey(t, m, "order-multiple").RemoteID != remote.items["order-multiple"] || !m.MultipleOrderProductOnly {
		t.Fatal("historical product-only order changed")
	}
}

func TestVariantPayloadParentAndHistoricOrderComposition(t *testing.T) {
	l, m, _ := existingFixture(t)
	if err := EnableVariantContinuation(l, &m, testAccount); err != nil {
		t.Fatal(err)
	}
	for i := range m.Operations {
		if m.Operations[i].Key == "variant-characteristic" || m.Operations[i].Key == "variant-one" || m.Operations[i].Key == "variant-two" {
			m.Operations[i].State = Created
			m.Operations[i].RemoteID = fmt.Sprintf("50000000-0000-0000-0000-%012d", i+1)
		}
	}
	c := mockClient(t, &fakeTransport{items: map[string]map[string]any{}})
	c.organization = "30000000-0000-0000-0000-000000000001"
	one, err := c.payload(opByKey(t, m, "variant-one"), m)
	if err != nil {
		t.Fatal(err)
	}
	two, err := c.payload(opByKey(t, m, "variant-two"), m)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := findID(m, "variant-parent")
	firstParent, ok1 := referenceID(one["product"], "product")
	secondParent, ok2 := referenceID(two["product"], "product")
	if !ok1 || !ok2 || firstParent != parent || secondParent != parent || one["externalCode"] == two["externalCode"] || one["characteristics"].([]any)[0].(map[string]any)["value"] == two["characteristics"].([]any)[0].(map[string]any)["value"] {
		t.Fatal("variant parent, identity or values collapsed")
	}
	old, err := c.payload(opByKey(t, m, "order-multiple"), m)
	if err != nil {
		t.Fatal(err)
	}
	positions := old["positions"].([]any)
	productA, _ := findID(m, "product-a")
	actual, ok := referenceID(positions[1].(map[string]any)["assortment"], "product")
	if !ok || actual != productA {
		t.Fatal("adding variants changed the existing order's expected composition")
	}
}

func TestVariantIndependentGetChecksParentAndCharacteristic(t *testing.T) {
	l, m, _ := existingFixture(t)
	if err := EnableVariantContinuation(l, &m, testAccount); err != nil {
		t.Fatal(err)
	}
	for i := range m.Operations {
		if m.Operations[i].Key == "variant-characteristic" || m.Operations[i].Key == "variant-one" {
			m.Operations[i].State = Created
			m.Operations[i].RemoteID = fmt.Sprintf("50000000-0000-0000-0000-%012d", i+1)
		}
	}
	c := mockClient(t, &fakeTransport{items: map[string]map[string]any{}})
	o := opByKey(t, m, "variant-one")
	body, err := c.payload(o, m)
	if err != nil {
		t.Fatal(err)
	}
	body["id"] = o.RemoteID
	body["accountId"] = testAccount
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/entity/variant/"+o.RemoteID) {
			t.Fatal("variant verification used an unexpected endpoint")
		}
		return jsonResponse(r, body), nil
	})
	if err = c.verify(context.Background(), o, o.RemoteID, m); err != nil {
		t.Fatal("valid variant was rejected", err)
	}
	body["product"] = reference("product", "60000000-0000-0000-0000-000000000001")
	if err = c.verify(context.Background(), o, o.RemoteID, m); err == nil {
		t.Fatal("variant with a different parent was accepted")
	}
}

func TestVariantWarehouseStockAndCustomerOrderPosition(t *testing.T) {
	l, m, _ := existingFixture(t)
	if err := EnableVariantContinuation(l, &m, testAccount); err != nil {
		t.Fatal(err)
	}
	for i := range m.Operations {
		if m.Operations[i].State == Planned {
			m.Operations[i].State = Created
			m.Operations[i].RemoteID = fmt.Sprintf("50000000-0000-0000-0000-%012d", i+1)
		}
	}
	c := mockClient(t, &fakeTransport{items: map[string]map[string]any{}})
	c.organization = "30000000-0000-0000-0000-000000000001"
	order := opByKey(t, m, "order-variant")
	body, err := c.payload(order, m)
	if err != nil {
		t.Fatal(err)
	}
	body["id"] = order.RemoteID
	body["accountId"] = testAccount
	body["updated"] = "2026-10-29 12:00:00.000"
	pos := body["positions"].([]any)[0].(map[string]any)
	pos["id"] = "60000000-0000-0000-0000-000000000001"
	pos["accountId"] = testAccount
	variant, _ := findID(m, "variant-one")
	if actual, ok := referenceID(pos["assortment"], "variant"); !ok || actual != variant || !equalNumber(pos["quantity"], 2) || !equalNumber(pos["reserve"], 1) {
		t.Fatal("variant order position lost its identity or values")
	}
	warehouse, _ := findID(m, "warehouse-a")
	row := reference("variant", variant)
	store := reference("store", warehouse)
	store["stock"], store["reserve"], store["inTransit"] = 8, 1, 0
	row["stockByStore"] = []any{store}
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/report/stock/bystore"):
			return jsonResponse(r, map[string]any{"meta": map[string]any{"size": 1, "limit": 100, "offset": 0}, "rows": []any{row}}), nil
		case strings.HasSuffix(r.URL.Path, "/positions"):
			return jsonResponse(r, map[string]any{"meta": map[string]any{"size": 1, "limit": 100, "offset": 0}, "rows": []any{pos}}), nil
		default:
			return jsonResponse(r, body), nil
		}
	})
	if err = c.verify(context.Background(), order, order.RemoteID, m); err != nil {
		t.Fatal("variant order was not independently verified", err)
	}
	observed, err := c.ObserveStock(context.Background(), m)
	if err != nil || observed["variant-one/warehouse-a"].Status != "STOCK_MATCH" || observed["variant-two/warehouse-a"].Status != "MISSING" {
		t.Fatal("variant warehouse stock or missing-row behavior incorrect", err)
	}
	delete(pos, "id")
	if err = c.verify(context.Background(), order, order.RemoteID, m); err == nil {
		t.Fatal("missing variant position ID accepted")
	}
}
