package orderprivacy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

type FinancialEvidenceProtector struct{ Vault *credentials.Vault }

func (p FinancialEvidenceProtector) SealFinancialEvidence(ctx context.Context, scope orders.Scope, period orders.RealizationPeriod, revision int64, raw []byte, fingerprint string) (orders.FinancialEvidenceRecord, error) {
	hash := sha256.Sum256(raw)
	record := orders.FinancialEvidenceRecord{Scope: scope, Period: period, CredentialRevision: revision, SourceFingerprint: fingerprint, RawDigest: hex.EncodeToString(hash[:]), BodyBytes: len(raw), ObservedAt: time.Now().UTC(), Chunks: make([][]byte, 0, (len(raw)+(64<<10)-1)/(64<<10))}
	if p.Vault == nil || scope.Validate() != nil || revision < 1 || len(raw) == 0 || len(raw) > 4<<20 {
		return record, orders.ErrBlocked
	}
	for start := 0; start < len(raw); start += 64 << 10 {
		if err := ctx.Err(); err != nil {
			return record, err
		}
		cipher, err := p.Vault.EncryptRecord(financialEvidenceScope(record, len(record.Chunks)), raw[start:min(start+(64<<10), len(raw))])
		if err != nil {
			return record, err
		}
		record.Chunks = append(record.Chunks, cipher)
	}
	return record, record.Validate()
}

func financialEvidenceScope(record orders.FinancialEvidenceRecord, index int) credentials.RecordScope {
	return credentials.RecordScope{Scope: credentials.Scope{Tenant: record.Scope.Tenant, Provider: "ozon", Account: record.Scope.OzonAccount}, Purpose: "finance-realization", RecordID: identityDigest(record.Scope.MoyskladAccount, strconv.Itoa(record.Period.Year), strconv.Itoa(record.Period.Month), record.SourceFingerprint, record.RawDigest, strconv.Itoa(index)), Revision: record.CredentialRevision}
}

func (p FinancialEvidenceProtector) OpenFinancialEvidence(ctx context.Context, record orders.FinancialEvidenceRecord) ([]byte, error) {
	if p.Vault == nil || record.Validate() != nil {
		return nil, orders.ErrBlocked
	}
	raw := make([]byte, 0, record.BodyBytes)
	for index, chunk := range record.Chunks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		plain, err := p.Vault.DecryptRecord(financialEvidenceScope(record, index), chunk)
		if err != nil {
			return nil, err
		}
		raw = append(raw, plain...)
	}
	hash := sha256.Sum256(raw)
	if len(raw) != record.BodyBytes || hex.EncodeToString(hash[:]) != record.RawDigest {
		return nil, orders.ErrStale
	}
	return raw, nil
}
