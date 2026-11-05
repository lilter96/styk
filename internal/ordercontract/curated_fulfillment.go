package ordercontract

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
)

// These are documentation-derived LOCAL test cases, never live provider facts
// or authorization to send their synthetic identities/business declarations.
//
//go:embed testdata/curated_20261105/*.json
var curatedFulfillmentFS embed.FS

const curatedFulfillmentRoot = "testdata/curated_20261105/"

type CuratedFulfillmentCase struct {
	ID                  string   `json:"id"`
	Endpoint            string   `json:"endpoint"`
	Direction           string   `json:"direction"`
	Fixture             string   `json:"fixture"`
	SHA256              string   `json:"sha256"`
	EvidenceKind        string   `json:"evidence_kind"`
	Sources             []string `json:"sources"`
	Coverage            string   `json:"coverage"`
	Prerequisites       []string `json:"prerequisites"`
	ExpectManifestValid bool     `json:"expect_manifest_valid"`
}

// CuratedFulfillmentCases returns independently decoded, hash-bound test cases.
// Source provenance remains separate from shape validation and live coverage.
func CuratedFulfillmentCases() ([]CuratedFulfillmentCase, error) {
	raw, err := curatedFulfillmentFS.ReadFile(curatedFulfillmentRoot + "cases.json")
	if err != nil {
		return nil, fmt.Errorf("read curated case index: %w", err)
	}
	var index struct {
		Version      int                      `json:"version"`
		GeneratedAt  string                   `json:"generated_at"`
		LiveRequests int                      `json:"live_provider_requests"`
		Cases        []CuratedFulfillmentCase `json:"cases"`
	}
	if CheckJSON(raw) != nil || json.Unmarshal(raw, &index) != nil || index.Version != 1 || index.LiveRequests != 0 || len(index.Cases) == 0 || len(index.Cases) > 100 {
		return nil, ErrManifest
	}
	sources, err := curatedSourceIDs()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(index.Cases))
	for _, c := range index.Cases {
		if seen[c.ID] || !c.valid() || !references(c.Sources, sources) {
			return nil, ErrManifest
		}
		if _, err = curatedCasePayload(c); err != nil {
			return nil, err
		}
		seen[c.ID] = true
	}
	return index.Cases, nil
}

func (c CuratedFulfillmentCase) valid() bool {
	return c.ID != "" && c.Endpoint != "" && slices.Contains([]string{"request", "response"}, c.Direction) && fs.ValidPath(c.Fixture) && path.Base(c.Fixture) == c.Fixture && path.Ext(c.Fixture) == ".json" && len(c.SHA256) == 64 && len(c.Sources) > 0 && c.Coverage != "" && len(c.Prerequisites) > 0 && slices.Contains([]string{"OFFICIAL_SCHEMA_GENERATED", "OFFICIAL_EXAMPLE_SANITIZED", "SCENARIO_SYNTHETIC", "THIRD_PARTY_SHAPE_DERIVED_SYNTHETIC"}, c.EvidenceKind)
}

// CuratedFulfillmentFixture supplies fresh bytes for a normal loopback provider.
// No credential, real buyer, signed label URL or network call is involved.
func CuratedFulfillmentFixture(id string) (CuratedFulfillmentCase, []byte, error) {
	cases, err := CuratedFulfillmentCases()
	if err != nil {
		return CuratedFulfillmentCase{}, nil, err
	}
	for _, c := range cases {
		if c.ID == id {
			raw, readErr := curatedCasePayload(c)
			return c, raw, readErr
		}
	}
	return CuratedFulfillmentCase{}, nil, ErrManifest
}

func curatedCasePayload(c CuratedFulfillmentCase) ([]byte, error) {
	raw, err := curatedFulfillmentFS.ReadFile(curatedFulfillmentRoot + c.Fixture)
	if err != nil {
		return nil, fmt.Errorf("read curated fixture: %w", err)
	}
	if CheckJSON(raw) != nil || digest(raw) != c.SHA256 {
		return nil, ErrManifest
	}
	return raw, nil
}

func curatedSourceIDs() (map[string]bool, error) {
	raw, err := curatedFulfillmentFS.ReadFile(curatedFulfillmentRoot + "provenance.json")
	if err != nil {
		return nil, fmt.Errorf("read curated provenance: %w", err)
	}
	var provenance struct {
		Sources []curatedSourceRef `json:"sources"`
	}
	if CheckJSON(raw) != nil || json.Unmarshal(raw, &provenance) != nil || len(provenance.Sources) == 0 || len(provenance.Sources) > 100 {
		return nil, ErrManifest
	}
	ids := make(map[string]bool, len(provenance.Sources))
	for _, source := range provenance.Sources {
		if source.ID == "" || ids[source.ID] || !source.validExcerpt() {
			return nil, ErrManifest
		}
		ids[source.ID] = true
	}
	return ids, nil
}

type curatedSourceRef struct {
	ID            string          `json:"id"`
	Excerpt       json.RawMessage `json:"excerpt"`
	ExcerptSHA256 string          `json:"excerpt_sha256"`
}

func (s curatedSourceRef) validExcerpt() bool {
	if len(s.Excerpt) == 0 {
		return s.ExcerptSHA256 == ""
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(s.Excerpt))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	normalized, err := json.Marshal(value)
	return err == nil && digest(normalized) == s.ExcerptSHA256
}
