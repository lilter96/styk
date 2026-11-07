//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/matching"
)

func TestProductMatchingNativeHashesMatchCanonicalConfirmation(t *testing.T) {
	db, _ := productMatchingFixture(t)
	tests := []struct {
		name, offer, sku string
		archived         *bool
	}{{name: "nil", offer: "A"}, {name: "unicode", offer: "<a>&\"\n\u2028\u2029", sku: "SKU<&>"}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sku *string
			if tc.sku != "" {
				sku = &tc.sku
			}
			args := []any{int64(11), int64(12), tc.offer, sku, tc.archived, tc.offer, "article", "code", "", "updated"}
			raw, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if err = db.Pool.QueryRow(context.Background(), `SELECT product_matching_observation_hash($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, args...).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if expected := fmt.Sprintf("%x", sha256.Sum256(raw)); got != expected {
				t.Fatalf("observation fingerprint mismatch: %s != %s", got, expected)
			}
			raw, err = json.Marshal(struct {
				Version      int    `json:"version"`
				Review       string `json:"review_id"`
				Acknowledged bool   `json:"acknowledged"`
				Explanation  string `json:"explanation"`
			}{1, "review", true, tc.offer})
			if err != nil {
				t.Fatal(err)
			}
			if err = db.Pool.QueryRow(context.Background(), `SELECT product_matching_confirmation_hash('review',$1)`, tc.offer).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if expected := fmt.Sprintf("%x", sha256.Sum256(raw)); got != expected {
				t.Fatalf("confirmation fingerprint mismatch: %s != %s", got, expected)
			}
		})
	}
}

func TestProductMatchingNativeClassificationMatchesDomain(t *testing.T) {
	db, scope := productMatchingFixture(t)
	active, archived := false, true
	targets := []domain.ExternalEntity{
		{Kind: "product", ID: "exact", Article: "A", Code: "A", Archived: &active},
		{Kind: "product", ID: "code", Code: "C", Archived: &active},
		{Kind: "product", ID: "duplicate-1", Article: "D", Archived: &active},
		{Kind: "product", ID: "duplicate-2", Article: "D", Archived: &active},
		{Kind: "product", ID: "unknown", Article: "U"},
		{Kind: "product", ID: "archived", Article: "R", Archived: &archived},
		{Kind: "product", ID: "parent", Article: "P", ParentID: "parent-id", Archived: &active},
		{Kind: "variant", ID: "variant", Article: "V", Archived: &active},
	}
	var products []domain.OzonProduct
	for i, offer := range []string{"A", "C", "D", "U", "R", "P", "V", "missing", " A ", ""} {
		products = append(products, domain.OzonProduct{ProductID: fmt.Sprint(i), OfferID: offer, Archived: &active})
	}
	now := time.Now().UTC()
	ctx := context.Background()
	_, err := db.SaveInventoryImport(ctx, domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: targets}, "native-targets")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.SaveOzonImport(ctx, domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: products}, "native-sources")
	if err != nil {
		t.Fatal(err)
	}
	var mi, oi int64
	if err = db.Pool.QueryRow(ctx, `SELECT (SELECT max(id) FROM source_imports WHERE source='moysklad'),(SELECT max(id) FROM source_imports WHERE source='ozon')`).Scan(&mi, &oi); err != nil {
		t.Fatal(err)
	}
	var sources []matching.Source
	var candidates []matching.Target
	for _, p := range products {
		sources = append(sources, matching.Source{Match: domain.MatchSource{Identity: domain.ProductIdentity{System: "ozon", Account: scope.OzonAccount, Kind: "product", ID: p.ProductID}, OfferID: p.OfferID}, Archived: p.Archived})
	}
	for _, p := range targets {
		candidates = append(candidates, matching.Target{Match: domain.MatchTarget{Identity: domain.ProductIdentity{System: "moysklad", Account: scope.MoyskladAccount, Kind: p.Kind, ID: p.ID}, Article: p.Article, Code: p.Code, ParentID: p.ParentID}, Archived: p.Archived})
	}
	var mappings []domain.Mapping
	for _, pair := range []struct{ source, target string }{{"7", "exact"}, {"6", "duplicate-1"}} {
		review, err := db.CreateMappingReview(ctx, domain.MappingReviewRequest{Tenant: scope.Tenant, OzonAccount: scope.OzonAccount, MoyskladAccount: scope.MoyskladAccount, ProductID: pair.source, TargetKind: "product", TargetID: pair.target, SessionID: "native-conformance", Evidence: domain.MappingOperatorAttested, Lifetime: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ConfirmMappingReview(ctx, domain.MappingConfirmationRequest{ReviewID: review.Review.ID, SessionID: "native-conformance", IdempotencyKey: "conformance-" + pair.source, Acknowledged: true, Explanation: "Native matching conformance fixture"}); err != nil {
			t.Fatal(err)
		}
		m := domain.Mapping{Tenant: scope.Tenant, Source: domain.ProductIdentity{System: "ozon", Account: scope.OzonAccount, Kind: "product", ID: pair.source}, Target: domain.ProductIdentity{System: "moysklad", Account: scope.MoyskladAccount, Kind: "product", ID: pair.target}}
		if err = db.Pool.QueryRow(ctx, `SELECT ozon_offer_id FROM product_mappings WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id=$3`, scope.Tenant, scope.OzonAccount, pair.source).Scan(&m.OfferID); err != nil {
			t.Fatal(err)
		}
		mappings = append(mappings, m)
	}
	for _, criterion := range []matching.Criterion{matching.ArticleOrCode, matching.Article, matching.Code} {
		t.Run(string(criterion), func(t *testing.T) {
			expected := matching.Preview(scope.Tenant, scope.MoyskladAccount, matching.Policy{ExactSKU: true, Criterion: criterion}, sources, candidates, mappings)
			rows, err := db.Pool.Query(ctx, `SELECT product_id,status,reason,shared_inventory FROM product_matching_rows($1,$2,$3,$4,$5,$6) ORDER BY product_id`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, mi, oi, criterion.Fields())
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			index := 0
			for rows.Next() {
				var id, status, reason string
				var shared bool
				if err = rows.Scan(&id, &status, &reason, &shared); err != nil {
					t.Fatal(err)
				}
				if index >= len(expected) || id != expected[index].ProductID || status != expected[index].Status || reason != expected[index].Reason || shared != expected[index].SharedInventory {
					t.Fatalf("domain mismatch at %s: %s/%s, expected %+v", id, status, reason, expected[index])
				}
				index++
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if index != len(expected) {
				t.Fatalf("missing rows: %d/%d", index, len(expected))
			}
		})
	}
}
