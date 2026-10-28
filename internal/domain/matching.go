package domain

import "time"

type ProductIdentity struct{ System, Account, Kind, ID string }
type MatchSource struct {
	Identity ProductIdentity
	OfferID  string
}
type MatchTarget struct {
	Identity      ProductIdentity
	Article, Code string
}
type MatchEvidence struct{ SourceField, TargetField, SourceValue, TargetValue, Normalized string }
type MatchCandidate struct {
	Target   ProductIdentity
	Evidence []MatchEvidence
}
type Mapping struct {
	Tenant              string
	Source, Target      ProductIdentity
	OfferID, Provenance string
	Version             int
	ConfirmedAt         time.Time
}
type MatchResult struct {
	Source          MatchSource
	Status          string
	Candidates      []MatchCandidate
	Confirmed       *Mapping
	SharedInventory bool
}
