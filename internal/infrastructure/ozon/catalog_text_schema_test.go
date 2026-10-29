package ozon

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogTextSchemaAcceptsObservedStringCaseAndRetainsSafetyGuards(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		attribute CategoryAttribute
		id        int64
		allowed   bool
	}{
		{"observed annotation", CategoryAttribute{ID: 4191, Name: "Аннотация", Type: "String"}, 4191, true},
		{"lowercase", CategoryAttribute{ID: 4191, Type: "string"}, 4191, true},
		{"uppercase existing text type", CategoryAttribute{ID: 4191, Type: "MULTILINE"}, 4191, true},
		{"dictionary", CategoryAttribute{ID: 4191, Type: "String", DictionaryID: 99}, 4191, false},
		{"complex", CategoryAttribute{ID: 4191, Type: "String", ComplexID: 7}, 4191, false},
		{"collection", CategoryAttribute{ID: 4191, Type: "String", Collection: true}, 4191, false},
		{"numeric", CategoryAttribute{ID: 4191, Type: "Integer"}, 4191, false},
		{"unknown type", CategoryAttribute{ID: 4191, Type: "Text"}, 4191, false},
		{"whitespace is not case", CategoryAttribute{ID: 4191, Type: " String "}, 4191, false},
		{"missing name attribute", CategoryAttribute{ID: 4191, Type: "String"}, 4180, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if actual := supportsTextAttribute([]CategoryAttribute{tc.attribute}, tc.id); actual != tc.allowed {
				t.Fatalf("allowed=%t expected=%t schema=%+v", actual, tc.allowed, tc.attribute)
			}
		})
	}
}

func TestCatalogDescriptionPatchUsesObservedCapitalizedProviderSchema(t *testing.T) {
	t.Parallel()
	var writes atomic.Int32
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == catalogSchemaPath {
			replyCatalogJSON(t, w, map[string]any{"result": []CategoryAttribute{{ID: 4191, Name: "Аннотация", Type: "String"}, {ID: 4180, Name: "Название", Type: "String"}}})
			return
		}
		if catalogFixtureRead(t, w, r) {
			return
		}
		if r.URL.Path != catalogPatchPath {
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		writes.Add(1)
		replyCatalogJSON(t, w, map[string]any{"task_id": 91})
	})
	command := catalogFixtureCommand()
	command.ExpectedTargetRevision = fixtureCatalogSnapshot(t, client).Products[0].Revision
	problems, err := client.ValidateCatalog(context.Background(), command)
	if err != nil || len(problems) != 0 {
		t.Fatalf("observed valid text schema rejected: %+v %v", problems, err)
	}
	if err := client.BindCatalogIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteCatalog(context.Background(), command)
	if err != nil || receipt.State != catalog.ReceiptAccepted || receipt.TaskID != "91" || writes.Load() != 1 {
		t.Fatalf("description dispatch: %+v %v writes=%d", receipt, err, writes.Load())
	}
}
