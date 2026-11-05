package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogAttributeEnumerationDoesNotInvalidateReviewedFacts(t *testing.T) {
	t.Parallel()
	const first = `{"attributes":[{"id":10,"values":[{"value":"a","dictionary_value_id":0}]},{"id":20,"values":[{"value":"b"}]}],"complex_attributes":[{"id":21837,"complex_id":100001,"values":[{"value":"video.mp4"}]},{"id":21845,"complex_id":100002,"values":[{"value":"https://example.test/video"}]}]}`
	tests := []struct {
		name, data string
		same       bool
	}{
		{"provider reorders attributes and JSON keys", `{"complex_attributes":[{"values":[{"value":"https://example.test/video"}],"complex_id":100002,"id":21845},{"values":[{"value":"video.mp4"}],"id":21837,"complex_id":100001}],"attributes":[{"values":[{"value":"b"}],"id":20},{"values":[{"dictionary_value_id":0,"value":"a"}],"id":10}]}`, true},
		{"actual attribute changes", `{"attributes":[{"id":10,"values":[{"value":"changed"}]}],"complex_attributes":[]}`, false},
		{"complex group changes", `{"attributes":[],"complex_attributes":[{"id":21837,"complex_id":999,"values":[{"value":"video.mp4"}]}]}`, false},
		{"unknown facts remain binding", `{"attributes":[],"complex_attributes":[],"future_provider_fact":true}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) { catalogFixtureRead(t, w, r) })
			left := catalogCanonicalProduct(t, client, first)
			right := catalogCanonicalProduct(t, client, tc.data)
			a, err := catalog.ProductFingerprint(left)
			if err != nil {
				t.Fatal(err)
			}
			b, err := catalog.ProductFingerprint(right)
			if err != nil {
				t.Fatal(err)
			}
			if (left.Revision == right.Revision) != tc.same || (a == b) != tc.same {
				t.Fatalf("reviewed facts equality differs: revision=%v facts=%v want=%v", left.Revision == right.Revision, a == b, tc.same)
			}
		})
	}
}

func catalogCanonicalProduct(t *testing.T, client *CatalogClient, raw string) catalog.Product {
	t.Helper()
	var attributes catalogWire
	if err := json.Unmarshal([]byte(raw), &attributes); err != nil {
		t.Fatal(err)
	}
	info := catalogWire{"id": json.RawMessage(`42`), "offer_id": json.RawMessage(`"OFFER"`), "sku": json.RawMessage(`43`), "name": json.RawMessage(`"name"`), "is_archived": json.RawMessage(`false`)}
	product, err := client.catalogProduct(context.Background(), info, attributes, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return product
}
