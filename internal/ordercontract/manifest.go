package ordercontract

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
)

//go:embed manifest.json
var manifestJSON []byte

var (
	ErrManifest = errors.New("invalid order contract manifest")
	fieldPath   = regexp.MustCompile(`^\$(\.[a-zA-Z_][a-zA-Z_0-9]*|\[\])*$`)
)

// ContractManifest returns a fresh copy of the reviewed embedded snapshot.
func ContractManifest() (Manifest, error) {
	return DecodeManifest(manifestJSON)
}

// DecodeManifest rejects incomplete classifications and inconsistent schemas.
func DecodeManifest(data []byte) (Manifest, error) {
	if len(data) == 0 || len(data) > 2*maxPayloadBytes {
		return Manifest{}, ErrManifest
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, ErrManifest
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Manifest{}, ErrManifest
	}
	if !manifest.valid() {
		return Manifest{}, ErrManifest
	}
	return manifest, nil
}

func (m Manifest) valid() bool {
	if m.Version != 1 || m.CheckedAt == "" || len(m.Sources) == 0 || len(m.Contracts) == 0 {
		return false
	}
	sources := make(map[string]bool, len(m.Sources))
	for _, source := range m.Sources {
		if source.ID == "" || sources[source.ID] || source.URL == "" || source.Evidence == "" || digest([]byte(source.Evidence)) != source.SHA256 {
			return false
		}
		sources[source.ID] = true
	}
	seen := make(map[string]bool, len(m.Contracts))
	for _, contract := range m.Contracts {
		key := contract.Endpoint + ":" + contract.Direction
		if seen[key] || !contract.valid(sources) {
			return false
		}
		seen[key] = true
	}
	return m.capabilitiesValid(sources)
}

func (c Contract) valid(sources map[string]bool) bool {
	if c.Endpoint == "" || c.Method == "" || c.Version == "" || c.Fixture == "" || !references(c.Sources, sources) {
		return false
	}
	if !slices.Contains([]string{"request", "response"}, c.Direction) || !slices.Contains([]string{"official_verified", "partial_unverified", "unverified"}, c.SchemaStatus) {
		return false
	}
	data, err := json.Marshal(c.Fields)
	if err != nil || digest(data) != c.SHA256 {
		return false
	}
	paths := make(map[string]bool, len(c.Fields))
	for _, field := range c.Fields {
		if paths[field.Path] || !field.valid(sources) || (c.Direction == "request" && field.ReadOnly) {
			return false
		}
		paths[field.Path] = true
	}
	_, err = c.tree()
	return err == nil && paths["$"]
}

func (f Field) valid(sources map[string]bool) bool {
	if !fieldPath.MatchString(f.Path) || !references(f.Sources, sources) || f.Semantics == "" || f.Destination == "" || f.Privacy == "" || f.Fixture == "" || f.Verification == "" {
		return false
	}
	if !slices.Contains([]string{"required", "optional", "conditional"}, f.Presence) || !slices.Contains([]string{"native", "custom", "structured", "read_only", "excluded"}, f.Classification) {
		return false
	}
	if len(f.Types) == 0 {
		return false
	}
	for _, kind := range f.Types {
		if !slices.Contains([]string{"object", "array", "string", "integer", "number", "boolean"}, kind) {
			return false
		}
	}
	return true
}

func references(ids []string, sources map[string]bool) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if !sources[id] {
			return false
		}
	}
	return true
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func manifestError() error {
	return fmt.Errorf("compile order field paths: %w", ErrManifest)
}
