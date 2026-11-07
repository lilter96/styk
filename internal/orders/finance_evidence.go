package orders

import (
	"context"
	"encoding/hex"
	"time"
)

type FinancialEvidenceRecord struct {
	Scope              Scope             `json:"scope"`
	Period             RealizationPeriod `json:"period"`
	CredentialRevision int64             `json:"credential_revision"`
	SourceFingerprint  string            `json:"source_fingerprint"`
	RawDigest          string            `json:"raw_digest"`
	BodyBytes          int               `json:"body_bytes"`
	ObservedAt         time.Time         `json:"observed_at"`
	Chunks             [][]byte          `json:"-"`
}

type FinancialEvidenceSealer interface {
	SealFinancialEvidence(context.Context, Scope, RealizationPeriod, int64, []byte, string) (FinancialEvidenceRecord, error)
}

func (record FinancialEvidenceRecord) Validate() error {
	if record.Scope.Validate() != nil || record.Period.Year < 2023 || record.Period.Month < 1 || record.Period.Month > 12 || record.CredentialRevision < 1 || record.ObservedAt.IsZero() || record.BodyBytes < 1 || record.BodyBytes > 4<<20 || len(record.Chunks) != (record.BodyBytes+(64<<10)-1)/(64<<10) {
		return ErrInvalid
	}
	for _, digest := range []string{record.SourceFingerprint, record.RawDigest} {
		if len(digest) != 64 {
			return ErrInvalid
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return ErrInvalid
		}
	}
	length := 0
	for _, chunk := range record.Chunks {
		if len(chunk) <= 28 || len(chunk) > (64<<10)+28 {
			return ErrInvalid
		}
		length += len(chunk) - 28
	}
	if length != record.BodyBytes {
		return ErrInvalid
	}
	return nil
}
