package ordercontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestAndEveryFieldHaveSanitizedFixtureCoverage(t *testing.T) {
	t.Parallel()
	manifest := testManifest(t)
	documented, err := os.ReadFile("../../docs/order-contract-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(documented, manifestJSON) {
		t.Fatal("documentation and executable manifest differ")
	}
	for _, contract := range manifest.Contracts {
		t.Run(contract.Endpoint+":"+contract.Direction, func(t *testing.T) {
			t.Parallel()
			checkContractFixture(t, manifest, contract)
		})
	}
}

func checkContractFixture(t *testing.T, manifest Manifest, contract Contract) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", contract.Fixture))
	if err != nil {
		t.Fatal(err)
	}
	report := manifest.Validate(contract.Endpoint, contract.Direction, data)
	if !report.Valid {
		t.Fatalf("fixture failed: %+v", report)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	paths := make(map[string]bool)
	fixturePaths(value, "$", paths)
	for _, field := range contract.Fields {
		if field.Fixture != contract.Fixture || !paths[field.Path] {
			t.Errorf("field has no fixture coverage: %s", field.Path)
		}
	}
}

func fixturePaths(value any, path string, paths map[string]bool) {
	paths[path] = true
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			fixturePaths(child, path+"."+key, paths)
		}
	case []any:
		for _, child := range value {
			fixturePaths(child, path+"[]", paths)
		}
	}
}

func TestManifestRejectsIncompleteOrTamperedRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "empty classification", mutate: func(m *Manifest) { m.Contracts[0].Fields[0].Classification = "" }},
		{name: "unknown source", mutate: func(m *Manifest) { m.Contracts[0].Sources = []string{"missing"} }},
		{name: "source hash mismatch", mutate: func(m *Manifest) { m.Sources[0].Evidence += "changed" }},
		{name: "unsafe path", mutate: func(m *Manifest) { m.Contracts[0].Fields[0].Path = "$.[email@example.invalid]" }},
		{name: "duplicate contract", mutate: func(m *Manifest) { m.Contracts = append(m.Contracts, m.Contracts[0]) }},
		{name: "duplicate path", mutate: func(m *Manifest) { m.Contracts[0].Fields = append(m.Contracts[0].Fields, m.Contracts[0].Fields[0]) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			manifest := testManifest(t)
			tc.mutate(&manifest)
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeManifest(data); !errors.Is(err, ErrManifest) {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func TestManifestCopiesDoNotShareMutableState(t *testing.T) {
	t.Parallel()
	first, second := testManifest(t), testManifest(t)
	first.Contracts[0].Fields[0].Types[0] = "changed"
	if second.Contracts[0].Fields[0].Types[0] == "changed" {
		t.Fatal("manifest snapshots share mutable field rules")
	}
}

func testManifest(t *testing.T) Manifest {
	t.Helper()
	manifest, err := ContractManifest()
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}
