package matching

import (
	"testing"

	"github.com/lilter96/styk/internal/domain"
)

func TestPreviewOnlyUniqueActiveExactMayConfirm(t *testing.T) {
	t.Parallel()
	active, archived := false, true
	source := Source{Match: domain.MatchSource{Identity: domain.ProductIdentity{System: "ozon", Account: "o", Kind: "product", ID: "1"}, OfferID: " A-1 "}, Name: "Чашка синяя 250 мл", Archived: &active}
	target := Target{Match: domain.MatchTarget{Identity: domain.ProductIdentity{System: "moysklad", Account: "m", Kind: "product", ID: "p"}, Article: "A-1", Name: source.Name}, Archived: &active}
	tests := []struct {
		name, status, reason string
		change               func(*Source, *[]Target, *[]domain.Mapping)
	}{
		{"exact", "exact", "exact_sku", func(_ *Source, _ *[]Target, _ *[]domain.Mapping) {}},
		{"same name different article", "review", "similar_name", func(_ *Source, ts *[]Target, _ *[]domain.Mapping) { (*ts)[0].Match.Article = "A-2" }},
		{"case remains meaningful", "review", "similar_name", func(s *Source, _ *[]Target, _ *[]domain.Mapping) { s.Match.OfferID = "a-1" }},
		{"duplicate article", "conflict", "duplicate_sku", func(_ *Source, ts *[]Target, _ *[]domain.Mapping) {
			other := (*ts)[0]
			other.Match.Identity.ID = "other"
			*ts = append(*ts, other)
		}},
		{"unknown lifecycle", "review", "lifecycle_unknown", func(_ *Source, ts *[]Target, _ *[]domain.Mapping) { (*ts)[0].Archived = nil }},
		{"archived", "review", "archived", func(_ *Source, ts *[]Target, _ *[]domain.Mapping) { (*ts)[0].Archived = &archived }},
		{"variant", "review", "variant_requires_review", func(_ *Source, ts *[]Target, _ *[]domain.Mapping) {
			(*ts)[0].Match.Identity.Kind = "variant"
			(*ts)[0].Match.ParentID = "parent"
		}},
		{"existing pair", "confirmed", "already_confirmed", func(s *Source, ts *[]Target, ms *[]domain.Mapping) {
			*ms = []domain.Mapping{{Tenant: "t", Source: s.Match.Identity, OfferID: s.Match.OfferID, Target: (*ts)[0].Match.Identity}}
		}},
		{"existing conflicting account", "conflict", "mapping_conflict", func(s *Source, ts *[]Target, ms *[]domain.Mapping) {
			id := (*ts)[0].Match.Identity
			id.Account = "other"
			*ms = []domain.Mapping{{Tenant: "t", Source: s.Match.Identity, OfferID: s.Match.OfferID, Target: id}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, targets, mappings := source, []Target{target}, []domain.Mapping{}
			tc.change(&s, &targets, &mappings)
			items := Preview("t", "m", Policy{ExactSKU: true, Names: true, AutoConfirm: true}, []Source{s}, targets, mappings)
			if len(items) != 1 || items[0].Status != tc.status || items[0].Reason != tc.reason {
				t.Fatalf("unexpected decision: %+v", items)
			}
		})
	}
}

func TestPreviewSharedInventoryAndDuplicateOffers(t *testing.T) {
	t.Parallel()
	active := false
	target := Target{Match: domain.MatchTarget{Identity: domain.ProductIdentity{System: "moysklad", Account: "m", Kind: "product", ID: "p"}, Article: "A", Code: "B"}, Archived: &active}
	a := Source{Match: domain.MatchSource{Identity: domain.ProductIdentity{System: "ozon", Account: "o", Kind: "product", ID: "1"}, OfferID: "A"}, Archived: &active}
	b := a
	b.Match.Identity.ID = "2"
	b.Match.OfferID = "B"
	mapping := domain.Mapping{Tenant: "t", Source: a.Match.Identity, Target: target.Match.Identity, OfferID: "A"}
	items := Preview("t", "m", Policy{ExactSKU: true}, []Source{b}, []Target{target}, []domain.Mapping{mapping})
	if items[0].Status != "exact" || !items[0].SharedInventory {
		t.Fatalf("shared inventory was blocked: %+v", items)
	}
	b.Match.OfferID = " A "
	items = Preview("t", "m", Policy{ExactSKU: true}, []Source{a, b}, []Target{target}, nil)
	for _, item := range items {
		if item.Status != "conflict" || item.Reason != "duplicate_offer" {
			t.Fatalf("duplicate offer accepted: %+v", item)
		}
	}
}
