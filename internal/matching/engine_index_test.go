package matching

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

// Compare the indexed engine with the original exhaustive mapping rules over
// mixtures of tenants, repeated source rows, shared targets, and raw offers.
func TestIndexedMappingsMatchExhaustiveRules(t *testing.T) {
	t.Parallel()
	random := rand.New(rand.NewSource(78419))
	offers := []string{"", "A", " A ", "\u00a0A\u00a0", "B", "C"}
	tenants := []string{"tenant", "other"}
	for iteration := range 3000 {
		var targets []domain.MatchTarget
		for range random.Intn(9) {
			targets = append(targets, randomTarget(random, offers))
		}
		var mappings []domain.Mapping
		for range random.Intn(20) {
			target := randomTarget(random, offers).Identity
			if len(targets) > 0 && random.Intn(2) == 0 {
				target = targets[random.Intn(len(targets))].Identity
			}
			mappings = append(mappings, domain.Mapping{
				Tenant: tenants[random.Intn(len(tenants))], Source: randomSource(random, offers).Identity,
				Target: target, OfferID: offers[random.Intn(len(offers))],
				Version: random.Intn(4) + 1, Provenance: fmt.Sprint(iteration), ConfirmedAt: time.Unix(int64(iteration), 0),
			})
		}
		source := randomSource(random, offers)
		tenant := tenants[random.Intn(len(tenants))]
		account := []string{"moy", "other-moy"}[random.Intn(2)]
		want := New(targets, nil).Match(tenant, source, account)
		if want.Status != Invalid {
			applyExhaustiveMappings(&want, mappings, tenant, account)
		}
		got := New(targets, mappings).Match(tenant, source, account)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: got %+v; want %+v; mappings %+v", iteration, got, want, mappings)
		}
	}
}

func randomSource(random *rand.Rand, offers []string) domain.MatchSource {
	return domain.MatchSource{
		Identity: domain.ProductIdentity{System: "ozon", Account: []string{"ozon", "other-ozon"}[random.Intn(2)], Kind: "product", ID: fmt.Sprint(random.Intn(4))},
		OfferID:  offers[random.Intn(len(offers))],
	}
}

func randomTarget(random *rand.Rand, offers []string) domain.MatchTarget {
	return domain.MatchTarget{
		Identity: domain.ProductIdentity{System: "moysklad", Account: []string{"moy", "other-moy"}[random.Intn(2)], Kind: []string{"product", "variant"}[random.Intn(2)], ID: fmt.Sprint(random.Intn(4))},
		Article:  offers[random.Intn(len(offers))], Code: offers[random.Intn(len(offers))],
	}
}

// This deliberately uses straightforward scans to specify the pre-index rules.
func applyExhaustiveMappings(r *domain.MatchResult, mappings []domain.Mapping, tenant, account string) {
	for _, candidate := range r.Candidates {
		for _, m := range mappings {
			if m.Tenant == tenant && m.Source != r.Source.Identity && m.Target == candidate.Target {
				r.SharedInventory = true
			}
		}
	}
	for _, m := range mappings {
		if m.Tenant != tenant || m.Source != r.Source.Identity {
			continue
		}
		copy := m
		r.Confirmed = &copy
		r.Status = AlreadyConfirmed
		if m.Target.Account != account || m.OfferID != r.Source.OfferID {
			r.Status = MappingConflict
		}
		for _, candidate := range r.Candidates {
			if candidate.Target != m.Target {
				r.Status = MappingConflict
			}
		}
		for _, other := range mappings {
			if other.Tenant == tenant && other.Source != m.Source && other.Target == m.Target {
				r.SharedInventory = true
			}
		}
		break
	}
	for _, m := range mappings {
		if m.Tenant == tenant && m.Source.Account == r.Source.Identity.Account && m.Source.ID != r.Source.Identity.ID && m.OfferID == r.Source.OfferID {
			r.Status = MappingConflict
		}
	}
}
