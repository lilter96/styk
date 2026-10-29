package moysklad

import (
	"context"
	"encoding/json"
	"testing"
)

func TestOrderMetadataAcceptsObservedEmptyMetaArray(t *testing.T) {
	simulator, adapter := newOrderSimulator(t, "metadata-meta")
	raw, err := json.Marshal(map[string]any{"meta": map[string]any{"href": simulator.server.URL + "/api/remap/1.2/entity/customerorder/metadata/attributes", "type": "attributemetadata", "size": 0, "limit": 1000, "offset": 0}})
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := adapter.readOrderAttributeDefinitions(context.Background(), raw)
	if err != nil || len(attributes) != 0 {
		t.Fatal("observed empty metadata collection was not accepted", err)
	}
}
func TestOrderMetadataRejectsSubstitutedCollectionOrigin(t *testing.T) {
	_, adapter := newOrderSimulator(t, "metadata-meta")
	raw := json.RawMessage(`{"meta":{"href":"http://127.0.0.1:9/private/attributes","size":0}}`)
	if _, err := adapter.readOrderAttributeDefinitions(context.Background(), raw); err == nil {
		t.Fatal("untrusted attribute collection accepted")
	}
}
func TestOrderMetadataDoesNotTreatMissingAsEmpty(t *testing.T) {
	_, adapter := newOrderSimulator(t, "metadata-meta")
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{}`)} {
		if _, err := adapter.readOrderAttributeDefinitions(context.Background(), raw); err == nil {
			t.Fatal("missing attribute collection became explicit empty")
		}
	}
}
