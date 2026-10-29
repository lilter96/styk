package matching

import (
	"fmt"
	"testing"

	"github.com/lilter96/styk/internal/domain"
)

// BenchmarkMatchingCatalog measures a full catalog evaluation including index
// construction. Half the sources are confirmed; a tenth share inventory with
// a different Ozon account. All values are synthetic and deterministic.
// Run: go test ./internal/matching -run '^$' -bench '^BenchmarkMatchingCatalog$' -benchmem -benchtime=1x -count=3
// For a CPU profile of 10,000: add -bench '^BenchmarkMatchingCatalog/10000$'
// -cpuprofile /tmp/styk-matching.cpu -o /tmp/styk-matching.test, then run
// go tool pprof -top /tmp/styk-matching.cpu.
// On an i5-13600KF with Go 1.27.3, median of three single iterations for the
// complete 10,000-product evaluation was 2.31 s before and 9.47 ms after the
// mapping index; allocation was 7.41 MB and 11.67 MB per evaluation. The old
// CPU profile attributed 99% cumulative time to mapping scans.
func BenchmarkMatchingCatalog(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			sources, targets, mappings := matchingBenchmarkFixture(size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				engine := New(targets, mappings)
				for _, source := range sources {
					result := engine.Match("tenant", source, "moy")
					if len(result.Candidates) != 1 {
						b.Fatalf("expected one candidate, got %d", len(result.Candidates))
					}
				}
			}
		})
	}
}

func matchingBenchmarkFixture(size int) ([]domain.MatchSource, []domain.MatchTarget, []domain.Mapping) {
	sources := make([]domain.MatchSource, 0, size)
	targets := make([]domain.MatchTarget, 0, size)
	mappings := make([]domain.Mapping, 0, size*3/5)
	for i := range size {
		id := fmt.Sprintf("%06d", i)
		source := domain.MatchSource{Identity: domain.ProductIdentity{System: "ozon", Account: "ozon", Kind: "product", ID: id}, OfferID: "offer-" + id}
		target := domain.MatchTarget{Identity: domain.ProductIdentity{System: "moysklad", Account: "moy", Kind: "product", ID: id}, Article: source.OfferID}
		sources = append(sources, source)
		targets = append(targets, target)
		if i%2 == 0 {
			mappings = append(mappings, domain.Mapping{Tenant: "tenant", Source: source.Identity, Target: target.Identity, OfferID: source.OfferID, Version: 1})
		}
		if i%10 == 0 {
			other := source.Identity
			other.Account = "other-ozon"
			mappings = append(mappings, domain.Mapping{Tenant: "tenant", Source: other, Target: target.Identity, OfferID: "shared-" + id, Version: 1})
		}
	}
	return sources, targets, mappings
}
