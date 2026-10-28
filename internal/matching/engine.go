package matching

import (
	"sort"
	"strings"

	"example.com/styk/internal/domain"
)

const (
	NoCandidate        = "no_candidate"
	UniqueCandidate    = "unique_candidate"
	MultipleCandidates = "multiple_candidates"
	AlreadyConfirmed   = "already_confirmed"
	MappingConflict    = "mapping_conflict"
	Invalid            = "invalid_or_insufficient"
)

// Normalize only trims surrounding whitespace; case and punctuation remain significant.
func Normalize(s string) string { return strings.TrimSpace(s) }

type indexEntry struct {
	target       domain.MatchTarget
	field, value string
}
type Engine struct {
	index    map[string][]indexEntry
	mappings []domain.Mapping
}

func New(targets []domain.MatchTarget, mappings []domain.Mapping) Engine {
	e := Engine{index: map[string][]indexEntry{}, mappings: mappings}
	for _, t := range targets {
		if t.Identity.System != "moysklad" || t.Identity.Account == "" || t.Identity.ID == "" || (t.Identity.Kind != "product" && t.Identity.Kind != "variant") {
			continue
		}
		for _, v := range []struct{ field, value string }{{"article", t.Article}, {"code", t.Code}} {
			if key := Normalize(v.value); key != "" {
				e.index[key] = append(e.index[key], indexEntry{t, v.field, v.value})
			}
		}
	}
	return e
}
func (e Engine) Match(tenant string, source domain.MatchSource, targetAccount string) domain.MatchResult {
	r := domain.MatchResult{Source: source, Candidates: []domain.MatchCandidate{}}
	if tenant == "" || source.Identity.System != "ozon" || source.Identity.Kind != "product" || source.Identity.Account == "" || source.Identity.ID == "" || targetAccount == "" || Normalize(source.OfferID) == "" {
		r.Status = Invalid
		return r
	}
	candidates := map[domain.ProductIdentity]*domain.MatchCandidate{}
	for _, entry := range e.index[Normalize(source.OfferID)] {
		if entry.target.Identity.Account != targetAccount {
			continue
		}
		identity := entry.target.Identity
		c := candidates[identity]
		if c == nil {
			c = &domain.MatchCandidate{Target: identity}
			candidates[identity] = c
		}
		c.Evidence = append(c.Evidence, domain.MatchEvidence{SourceField: "offer_id", TargetField: entry.field, SourceValue: source.OfferID, TargetValue: entry.value, Normalized: Normalize(source.OfferID)})
	}
	for _, c := range candidates {
		sort.Slice(c.Evidence, func(i, j int) bool { return c.Evidence[i].TargetField < c.Evidence[j].TargetField })
		r.Candidates = append(r.Candidates, *c)
	}
	sort.Slice(r.Candidates, func(i, j int) bool {
		a, b := r.Candidates[i].Target, r.Candidates[j].Target
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	switch len(r.Candidates) {
	case 0:
		r.Status = NoCandidate
	case 1:
		r.Status = UniqueCandidate
	default:
		r.Status = MultipleCandidates
	}
	e.applyMappings(&r, tenant, targetAccount)
	return r
}

// applyMappings adds conflict and shared-inventory information without selecting candidates.
func (e Engine) applyMappings(r *domain.MatchResult, tenant, targetAccount string) {
	source := r.Source
	for _, candidate := range r.Candidates {
		for _, m := range e.mappings {
			if m.Tenant == tenant && m.Source != source.Identity && m.Target == candidate.Target {
				r.SharedInventory = true
			}
		}
	}
	for _, m := range e.mappings {
		if m.Tenant == tenant && m.Source == source.Identity {
			copy := m
			r.Confirmed = &copy
			r.Status = AlreadyConfirmed
			if m.Target.Account != targetAccount || m.OfferID != source.OfferID {
				r.Status = MappingConflict
			}
			for _, c := range r.Candidates {
				if c.Target != m.Target {
					r.Status = MappingConflict
					break
				}
			}
			for _, other := range e.mappings {
				if other.Tenant == tenant && other.Source != m.Source && other.Target == m.Target {
					r.SharedInventory = true
				}
			}
			break
		}
	}
	for _, m := range e.mappings {
		if m.Tenant == tenant && m.Source.Account == source.Identity.Account && m.Source.ID != source.Identity.ID && m.OfferID == source.OfferID {
			r.Status = MappingConflict
		}
	}
}
