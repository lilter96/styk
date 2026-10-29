package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogEmptyDiscoveryChecksBothVisibilityGroups(t *testing.T) {
	t.Parallel()
	var groups atomic.Int32
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != productPath {
			t.Errorf("unexpected empty traversal path %s", r.URL.Path)
			return
		}
		var body struct {
			Filter struct {
				Visibility string `json:"visibility"`
			} `json:"filter"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		switch body.Filter.Visibility {
		case "ALL":
			groups.Or(1)
		case "ARCHIVED":
			groups.Or(2)
		default:
			t.Error("unknown visibility")
		}
		replyCatalogJSON(t, w, map[string]any{"result": map[string]any{"items": []any{}, "total_items": 0, "last_id": ""}})
	})
	snapshot, err := client.ReadCatalog(context.Background(), catalog.ReadRequest{Scope: catalogFixtureScope, Account: catalogFixtureIdentity().Account, Limit: 1})
	if err != nil || !snapshot.Complete || len(snapshot.Products) != 0 || groups.Load() != 3 {
		t.Fatalf("incomplete visibility traversal: %+v %v groups=%d", snapshot, err, groups.Load())
	}
}

func TestCatalogDiscoveryCursorPreservesVisibilityAndExactCount(t *testing.T) {
	t.Parallel()
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			LastID string `json:"last_id"`
			Filter struct {
				Visibility string `json:"visibility"`
			} `json:"filter"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Filter.Visibility == "ARCHIVED" {
			replyCatalogJSON(t, w, map[string]any{"result": map[string]any{"items": []any{}, "total_items": 0}})
			return
		}
		pid, last := 42, "cursor-a"
		if body.LastID == "cursor-a" {
			pid, last = 44, "cursor-b"
		}
		replyCatalogJSON(t, w, map[string]any{"result": map[string]any{"items": []any{map[string]any{"product_id": pid, "offer_id": "OFFER"}}, "total_items": 2, "last_id": last}})
	})
	request := catalog.ReadRequest{Scope: catalogFixtureScope, Account: catalogFixtureIdentity().Account, Limit: 1}
	for index, wanted := range []string{"42", "44", ""} {
		identities, complete, cursor, err := client.catalogIdentities(context.Background(), request)
		if err != nil || complete != (index == 2) {
			t.Fatalf("page=%d complete=%t err=%v", index, complete, err)
		}
		if wanted != "" && (len(identities) != 1 || identities[0].ID != wanted || cursor == "") {
			t.Fatalf("page %d lost identity or continuation: %+v", index, identities)
		}
		request.Cursor = cursor
	}
}

func TestCatalogObservedEmptyUnknownAndAbsentRemainDistinct(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		data     json.RawMessage
		expected catalog.FieldState
	}{{"absent", nil, catalog.StateAbsent}, {"null", json.RawMessage(`null`), catalog.StateUnknown}, {"empty text", json.RawMessage(`""`), catalog.StateEmpty}, {"empty array whitespace", json.RawMessage(`[ ]`), catalog.StateEmpty}, {"known text", json.RawMessage(`"value"`), catalog.StateKnown}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wire := catalogWire{}
			if tc.data != nil {
				wire["value"] = tc.data
			}
			value := wireValue(wire, "value")
			if value.State != tc.expected || catalog.ValidateValue(value) != nil {
				t.Fatalf("state lost: %+v", value)
			}
		})
	}
}

func TestCatalogSchemaNeverAssumesMissingSafetyFlagsAreFalse(t *testing.T) {
	t.Parallel()
	var attribute CategoryAttribute
	if err := json.Unmarshal([]byte(`{"id":4180,"type":"string"}`), &attribute); err == nil {
		t.Fatal("missing schema flags accepted as false")
	}
	var node CategoryNode
	if err := json.Unmarshal([]byte(`{"description_category_id":7,"type_id":8,"children":[]}`), &node); err == nil {
		t.Fatal("missing disabled flag treated as creatable")
	}
}
