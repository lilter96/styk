package orders

import (
	"context"
	"strings"
	"time"
)

type AccrualKind string

const (
	AccrualTypes    AccrualKind = "TYPES"
	AccrualByDay    AccrualKind = "BY_DAY"
	AccrualPostings AccrualKind = "POSTINGS"
)

type AccrualRequest struct {
	Kind           AccrualKind `json:"kind"`
	Date           string      `json:"date,omitempty"`
	PostingNumbers []string    `json:"posting_numbers,omitempty"`
}

// AccrualFact retains a typed provider path without inventing an accounting
// allocation. Presence on objects/arrays also preserves null versus empty.
type AccrualFact struct {
	Path     string   `json:"path"`
	Kind     string   `json:"kind"`
	Presence Presence `json:"presence"`
	Text     *string  `json:"text,omitempty"`
	Number   *Decimal `json:"number,omitempty"`
	Size     *int     `json:"size,omitempty"`
}

type AccrualUnknownField struct {
	Path        string         `json:"path"`
	Kind        string         `json:"kind"`
	Value       Field[Decimal] `json:"value"`
	ValueDigest string         `json:"value_digest"`
}

type AccrualType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AccrualReport struct {
	Scope           Scope                 `json:"scope"`
	SourceFamily    string                `json:"source_family"`
	Kind            AccrualKind           `json:"kind"`
	Request         AccrualRequest        `json:"request"`
	Facts           []AccrualFact         `json:"facts"`
	Types           []AccrualType         `json:"types,omitempty"`
	UnknownFields   []AccrualUnknownField `json:"unknown_fields"`
	Complete        bool                  `json:"complete"`
	Pages           int                   `json:"pages"`
	RecordCount     int                   `json:"record_count"`
	ContractVersion string                `json:"contract_version"`
	Fingerprint     string                `json:"fingerprint"`
	ObservedAt      time.Time             `json:"observed_at"`
	Blockers        []Validation          `json:"blockers"`
}

type AccrualReader interface {
	ReadAccrualReport(context.Context, Scope, AccrualRequest) (AccrualReport, error)
}

type AccrualReadError struct {
	Code  string
	Cause error
}

func (e *AccrualReadError) Error() string { return e.Code }
func (e *AccrualReadError) Unwrap() error { return e.Cause }

func (request AccrualRequest) Validate(now time.Time) error {
	switch request.Kind {
	case AccrualTypes:
		if request.Date != "" || len(request.PostingNumbers) != 0 {
			return ErrInvalid
		}
	case AccrualByDay:
		day, err := time.Parse("2006-01-02", request.Date)
		if err != nil || day.Before(time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)) || day.After(now.UTC()) || len(request.PostingNumbers) != 0 {
			return ErrInvalid
		}
	case AccrualPostings:
		if request.Date != "" || len(request.PostingNumbers) < 1 || len(request.PostingNumbers) > 200 {
			return ErrInvalid
		}
		seen := make(map[string]bool, len(request.PostingNumbers))
		for _, id := range request.PostingNumbers {
			if !validID(id) || len(id) > 200 || strings.ContainsAny(id, "\r\n\x00") || seen[id] {
				return ErrInvalid
			}
			seen[id] = true
		}
	default:
		return ErrInvalid
	}
	return nil
}
