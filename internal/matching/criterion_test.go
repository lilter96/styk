package matching

import (
	"github.com/lilter96/styk/internal/domain"
	"testing"
)

func TestCriterionRestrictsExactEvidence(t *testing.T) {
	t.Parallel()
	active := false
	source := Source{Match: domain.MatchSource{Identity: domain.ProductIdentity{System: "ozon", Account: "ozon", Kind: "product", ID: "one"}, OfferID: "A"}, Archived: &active}
	targets := []Target{{Match: domain.MatchTarget{Identity: domain.ProductIdentity{System: "moysklad", Account: "moy", Kind: "product", ID: "article"}, Article: "A"}, Archived: &active}, {Match: domain.MatchTarget{Identity: domain.ProductIdentity{System: "moysklad", Account: "moy", Kind: "product", ID: "code"}, Code: "A"}, Archived: &active}}
	tests := []struct {
		name           string
		criterion      Criterion
		status, target string
	}{{"both", ArticleOrCode, "conflict", ""}, {"article", Article, "exact", "article"}, {"code", Code, "exact", "code"}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			item := Preview("tenant", "moy", Policy{ExactSKU: true, Criterion: tc.criterion}, []Source{source}, targets, nil)[0]
			if item.Status != tc.status {
				t.Fatalf("status: %+v", item)
			}
			if tc.target != "" && item.Candidates[0].ID != tc.target {
				t.Fatalf("wrong candidate: %+v", item)
			}
		})
	}
}
