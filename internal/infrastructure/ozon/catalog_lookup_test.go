package ozon

import (
	"context"
	"net/http"
	"testing"
)

func TestCatalogCreationLookupDistinguishesAbsentAndExistingOffer(t *testing.T) {
	t.Parallel()
	for _, absent := range []bool{true, false} {
		t.Run(map[bool]string{true: "absent", false: "existing"}[absent], func(t *testing.T) {
			t.Parallel()
			client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
				if absent {
					if r.URL.Path != productInfoPath {
						t.Error("absent lookup requested details")
					}
					replyCatalogJSON(t, w, map[string]any{"items": []any{}})
					return
				}
				if !catalogFixtureRead(t, w, r) {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			})
			snapshot, err := client.LookupCatalogCreate(context.Background(), catalogFixtureCreate())
			if err != nil || !snapshot.Complete || snapshot.Scope != catalogFixtureScope || snapshot.Account != catalogFixtureIdentity().Account {
				t.Fatalf("lookup: %+v %v", snapshot, err)
			}
			if absent && len(snapshot.Products) != 0 {
				t.Fatal("absent offer fabricated a target")
			}
			if !absent && (len(snapshot.Products) != 1 || snapshot.Products[0].Identity.ID != "42") {
				t.Fatal("existing exact offer lost")
			}
		})
	}
}
