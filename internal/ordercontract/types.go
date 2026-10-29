// Package ordercontract validates bounded, sanitized order contract snapshots.
// It performs no network calls and never returns source payload values.
package ordercontract

// Manifest records evidence and a deliberately explicit field allowlist.
type Manifest struct {
	Version      int          `json:"version"`
	CheckedAt    string       `json:"checked_at"`
	Sources      []Source     `json:"sources"`
	Contracts    []Contract   `json:"contracts"`
	Capabilities []Capability `json:"capabilities"`
	Limitations  []string     `json:"limitations"`
}

// Source hashes the stated evidence, not an inaccessible remote schema.
type Source struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	CheckedAt string `json:"checked_at"`
	Status    string `json:"status"`
	SHA256    string `json:"sha256"`
	HashBasis string `json:"hash_basis"`
	Evidence  string `json:"evidence"`
}

// Contract identifies a versioned endpoint and one request or response shape.
type Contract struct {
	Endpoint     string   `json:"endpoint"`
	Method       string   `json:"method"`
	Version      string   `json:"version"`
	Direction    string   `json:"direction"`
	SchemaStatus string   `json:"schema_status"`
	Coverage     string   `json:"coverage"`
	Sources      []string `json:"sources"`
	SHA256       string   `json:"sha256"`
	Fixture      string   `json:"fixture"`
	Fields       []Field  `json:"fields"`
}

// Field preserves presence separately from JSON null and from zero values.
type Field struct {
	Path           string   `json:"path"`
	Types          []string `json:"types"`
	Presence       string   `json:"presence"`
	Nullable       bool     `json:"nullable"`
	Sources        []string `json:"sources"`
	Verification   string   `json:"verification"`
	Semantics      string   `json:"semantics"`
	Destination    string   `json:"destination"`
	Classification string   `json:"classification"`
	ReadOnly       bool     `json:"read_only"`
	Privacy        string   `json:"privacy"`
	Fixture        string   `json:"fixture"`
}

// Issue contains only schema-defined paths or hashes of unknown key names.
type Issue struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

// Report.Valid proves the allowlisted shape only; inspect SchemaVerified too.
type Report struct {
	Valid          bool     `json:"valid"`
	SchemaVerified bool     `json:"schema_verified"`
	Issues         []Issue  `json:"issues"`
	DriftPaths     []string `json:"drift_paths"`
}
