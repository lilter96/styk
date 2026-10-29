package matching

import (
	"sort"
	"strings"

	"github.com/lilter96/styk/internal/domain"
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
type mappingSourceKey struct {
	tenant string
	source domain.ProductIdentity
}

type mappingTargetKey struct {
	tenant string
	target domain.ProductIdentity
}

type mappingOfferKey struct{ tenant, account, offer string }

type distinctSources struct {
	first     domain.ProductIdentity
	present   bool
	different bool
}

func (s distinctSources) add(source domain.ProductIdentity) distinctSources {
	if !s.present {
		s.first, s.present = source, true
		return s
	}
	if s.first != source {
		s.different = true
	}
	return s
}

func (s distinctSources) hasOther(source domain.ProductIdentity) bool {
	return s.present && (s.different || s.first != source)
}

type distinctIDs struct {
	first     string
	present   bool
	different bool
}

func (s distinctIDs) add(id string) distinctIDs {
	if !s.present {
		s.first, s.present = id, true
		return s
	}
	if s.first != id {
		s.different = true
	}
	return s
}

func (s distinctIDs) hasOther(id string) bool {
	return s.present && (s.different || s.first != id)
}

// Engine is a snapshot of targets and confirmed mappings for an evaluation.
type Engine struct {
	index         map[string][]indexEntry
	mappedSources map[mappingSourceKey]domain.Mapping
	mappedTargets map[mappingTargetKey]distinctSources
	mappedOffers  map[mappingOfferKey]distinctIDs
}

func New(targets []domain.MatchTarget, mappings []domain.Mapping) Engine {
	e := Engine{
		index:         make(map[string][]indexEntry, len(targets)),
		mappedSources: make(map[mappingSourceKey]domain.Mapping, len(mappings)),
		mappedTargets: make(map[mappingTargetKey]distinctSources, len(mappings)),
		mappedOffers:  make(map[mappingOfferKey]distinctIDs, len(mappings)),
	}
	e.indexMappings(mappings)
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

// indexMappings preserves the first confirmation for a source. For target and
// offer conflicts, two distinct sources are sufficient to answer whether any
// mapping belongs to someone else, regardless of repeated input rows.
func (e Engine) indexMappings(mappings []domain.Mapping) {
	for _, m := range mappings {
		source := mappingSourceKey{m.Tenant, m.Source}
		if _, exists := e.mappedSources[source]; !exists {
			e.mappedSources[source] = m
		}
		target := mappingTargetKey{m.Tenant, m.Target}
		e.mappedTargets[target] = e.mappedTargets[target].add(m.Source)
		offer := mappingOfferKey{m.Tenant, m.Source.Account, m.OfferID}
		e.mappedOffers[offer] = e.mappedOffers[offer].add(m.Source.ID)
	}
}

// applyMappings adds conflict and shared-inventory information without selecting candidates.
func (e Engine) applyMappings(r *domain.MatchResult, tenant, targetAccount string) {
	source := r.Source
	for _, candidate := range r.Candidates {
		if e.mappedTargets[mappingTargetKey{tenant, candidate.Target}].hasOther(source.Identity) {
			r.SharedInventory = true
		}
	}
	m, confirmed := e.mappedSources[mappingSourceKey{tenant, source.Identity}]
	if confirmed {
		r.Confirmed = &m
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
		if e.mappedTargets[mappingTargetKey{tenant, m.Target}].hasOther(m.Source) {
			r.SharedInventory = true
		}
	}
	if e.mappedOffers[mappingOfferKey{tenant, source.Identity.Account, source.OfferID}].hasOther(source.Identity.ID) {
		r.Status = MappingConflict
	}
}
