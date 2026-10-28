package matching

import (
	"encoding/json"
	"example.com/styk/internal/domain"
	"testing"
)

func src(id, offer string) domain.MatchSource {
	return domain.MatchSource{Identity: domain.ProductIdentity{System: "ozon", Account: "o", Kind: "product", ID: id}, OfferID: offer}
}
func tgt(kind, id, article, code string) domain.MatchTarget {
	return domain.MatchTarget{Identity: domain.ProductIdentity{System: "moysklad", Account: "m", Kind: kind, ID: id}, Article: article, Code: code}
}
func TestMatching(t *testing.T) {
	s := src("1", " A-01 ")
	a := tgt("product", "a", "A-01", "")
	v := tgt("variant", "v", "A-01", "")
	different := tgt("product", "other", "A-01", "")
	different.Identity.Account = "other"
	cases := []struct {
		name    string
		targets []domain.MatchTarget
		want    string
		n       int
	}{{"unique", []domain.MatchTarget{a, different}, UniqueCandidate, 1}, {"duplicates", []domain.MatchTarget{a, v}, MultipleCandidates, 2}, {"missing article", []domain.MatchTarget{tgt("product", "a", "", "")}, NoCandidate, 0}, {"different punctuation", []domain.MatchTarget{tgt("product", "a", "A01", "")}, NoCandidate, 0}, {"different case", []domain.MatchTarget{tgt("product", "a", "a-01", "")}, NoCandidate, 0}, {"conflicting evidence", []domain.MatchTarget{a, tgt("variant", "v", "", "A-01")}, MultipleCandidates, 2}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(tc.targets, nil).Match("tenant", s, "m")
			if r.Status != tc.want || len(r.Candidates) != tc.n {
				t.Fatalf("%+v", r)
			}
			b, _ := json.Marshal(r)
			b2, _ := json.Marshal(New(tc.targets, nil).Match("tenant", s, "m"))
			if string(b) != string(b2) {
				t.Fatal("nondeterministic")
			}
		})
	}
	if New(nil, nil).Match("tenant", src("1", " "), "m").Status != Invalid {
		t.Fatal("missing offer accepted")
	}
}
func TestConfirmedAndShared(t *testing.T) {
	a := tgt("product", "a", "A", "")
	b := tgt("variant", "b", "", "A")
	s := src("1", "A")
	mapping := domain.Mapping{Tenant: "t", Source: s.Identity, Target: a.Identity, OfferID: "A", Version: 1}
	other := domain.Mapping{Tenant: "t", Source: src("2", "B").Identity, Target: a.Identity, OfferID: "B", Version: 1}
	r := New([]domain.MatchTarget{a}, []domain.Mapping{mapping, other}).Match("t", s, "m")
	if r.Status != AlreadyConfirmed || !r.SharedInventory || r.Confirmed == nil {
		t.Fatalf("%+v", r)
	}
	r = New([]domain.MatchTarget{a, b}, []domain.Mapping{mapping}).Match("t", s, "m")
	if r.Status != MappingConflict || len(r.Candidates) != 2 {
		t.Fatalf("%+v", r)
	}
	r = New(nil, []domain.Mapping{mapping}).Match("t", s, "m")
	if r.Status != AlreadyConfirmed {
		t.Fatalf("mapping lost: %+v", r)
	}
	r = New([]domain.MatchTarget{a}, []domain.Mapping{other}).Match("t", s, "m")
	if !r.SharedInventory || r.Status != UniqueCandidate {
		t.Fatalf("shared candidate: %+v", r)
	}
	if New([]domain.MatchTarget{a}, []domain.Mapping{mapping}).Match("other", s, "m").Status != UniqueCandidate {
		t.Fatal("tenant isolation failed")
	}
}
