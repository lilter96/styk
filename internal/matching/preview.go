package matching

import (
	"sort"
	"strings"
	"unicode"

	"github.com/lilter96/styk/internal/domain"
)

type Policy struct {
	AfterImport bool  `json:"after_import"`
	Revision    int64 `json:"revision"`
	ExactSKU    bool  `json:"exact_sku"`
	Names       bool  `json:"names"`
	AutoConfirm bool  `json:"auto_confirm"`
}
type Source struct {
	Match    domain.MatchSource `json:"match"`
	Name     string             `json:"name"`
	Archived *bool              `json:"archived"`
}
type Target struct {
	Match    domain.MatchTarget `json:"match"`
	Archived *bool              `json:"archived"`
}
type Suggestion struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	Article  string  `json:"article"`
	Evidence string  `json:"evidence"`
	Score    float64 `json:"score"`
}
type Item struct {
	ProductID       string       `json:"product_id"`
	OfferID         string       `json:"offer_id"`
	Name            string       `json:"name"`
	Status          string       `json:"status"`
	Reason          string       `json:"reason"`
	Candidates      []Suggestion `json:"candidates"`
	SharedInventory bool         `json:"shared_inventory"`
}

// Preview is deterministic. Names can add review candidates, never exact ones.
func Preview(tenant, account string, policy Policy, sources []Source, targets []Target, mappings []domain.Mapping) []Item {
	plain := make([]domain.MatchTarget, 0, len(targets))
	indexed := make(map[domain.ProductIdentity]Target, len(targets))
	offers := make(map[string]int, len(sources))
	for _, t := range targets {
		plain = append(plain, t.Match)
		indexed[t.Match.Identity] = t
	}
	for _, s := range sources {
		offers[Normalize(s.Match.OfferID)]++
	}
	engine := New(plain, mappings)
	names := indexNames(targets)
	result := make([]Item, 0, len(sources))
	for _, s := range sources {
		match := engine.Match(tenant, s.Match, account)
		item := exactItem(s, match, indexed, policy)
		if offers[Normalize(s.Match.OfferID)] > 1 && match.Confirmed == nil {
			item.Status, item.Reason = "conflict", "duplicate_offer"
		}
		if item.Status == "unmatched" && policy.Names {
			addNameSuggestions(&item, s.Name, names)
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ProductID < result[j].ProductID })
	return result
}

func exactItem(s Source, m domain.MatchResult, index map[domain.ProductIdentity]Target, p Policy) Item {
	item := Item{ProductID: s.Match.Identity.ID, OfferID: s.Match.OfferID, Name: s.Name, Status: "unmatched", Reason: "no_match", Candidates: []Suggestion{}, SharedInventory: m.SharedInventory}
	if m.Status == MappingConflict {
		item.Status, item.Reason = "conflict", "mapping_conflict"
		return item
	}
	if m.Confirmed != nil {
		item.Status, item.Reason = "confirmed", "already_confirmed"
		return item
	}
	if !p.ExactSKU {
		return item
	}
	for _, c := range m.Candidates {
		t := index[c.Target]
		item.Candidates = append(item.Candidates, Suggestion{ID: c.Target.ID, Kind: c.Target.Kind, Name: t.Match.Name, Article: t.Match.Article, Evidence: "exact_sku", Score: 1})
	}
	if len(item.Candidates) > 1 {
		item.Status, item.Reason = "conflict", "duplicate_sku"
		return item
	}
	if len(item.Candidates) == 0 {
		return item
	}
	item.Status, item.Reason = "exact", "exact_sku"
	t := index[m.Candidates[0].Target]
	if s.Archived == nil || t.Archived == nil {
		item.Status, item.Reason = "review", "lifecycle_unknown"
		return item
	}
	if *s.Archived || *t.Archived {
		item.Status, item.Reason = "review", "archived"
		return item
	}
	if t.Match.Identity.Kind == "variant" || t.Match.ParentID != "" {
		item.Status, item.Reason = "review", "variant_requires_review"
	}
	return item
}

func nameTokens(name string) map[string]bool {
	text := strings.Map(func(r rune) rune {
		if r == 'ё' || r == 'Ё' {
			return 'е'
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, name)
	tokens := strings.Fields(text)
	result := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		result[token] = true
	}
	return result
}
func similarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	common := 0
	for word := range a {
		if b[word] {
			common++
		}
	}
	return float64(common) / float64(len(a)+len(b)-common)
}
func addNameSuggestions(item *Item, name string, targets []nameTarget) {
	words := nameTokens(name)
	for _, t := range targets {
		if t.Archived != nil && *t.Archived {
			continue
		}
		score := similarity(words, t.words)
		if score < 0.5 {
			continue
		}
		item.Candidates = append(item.Candidates, Suggestion{ID: t.Match.Identity.ID, Kind: t.Match.Identity.Kind, Name: t.Match.Name, Article: t.Match.Article, Evidence: "similar_name", Score: score})
	}
	sort.Slice(item.Candidates, func(i, j int) bool {
		a, b := item.Candidates[i], item.Candidates[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	if len(item.Candidates) > 3 {
		item.Candidates = item.Candidates[:3]
	}
	if len(item.Candidates) > 0 {
		item.Status, item.Reason = "review", "similar_name"
	}
}

type nameTarget struct {
	Target
	words map[string]bool
}

func indexNames(targets []Target) []nameTarget {
	result := make([]nameTarget, 0, len(targets))
	for _, target := range targets {
		result = append(result, nameTarget{Target: target, words: nameTokens(target.Match.Name)})
	}
	return result
}
