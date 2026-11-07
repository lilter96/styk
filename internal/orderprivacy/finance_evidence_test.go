package orderprivacy

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

func TestFinancialEvidenceEncryptionIsLosslessScopedAndHiddenFromJSON(t *testing.T) {
	t.Parallel()
	vault, err := credentials.New(bytes.Repeat([]byte{83}, 32))
	if err != nil {
		t.Fatal(err)
	}
	p := FinancialEvidenceProtector{Vault: vault}
	raw := bytes.Repeat([]byte("PRIVATE_FINANCIAL_HEADER"), 6000)
	record, err := p.SealFinancialEvidence(context.Background(), orders.Scope{Tenant: "tenant", OzonAccount: "seller", MoyskladAccount: "native"}, orders.RealizationPeriod{Year: 2026, Month: 10}, 3, raw, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil || len(record.Chunks) < 2 {
		t.Fatal("large evidence was not chunked", err)
	}
	actual, err := p.OpenFinancialEvidence(context.Background(), record)
	if err != nil || !bytes.Equal(actual, raw) {
		t.Fatal("raw evidence not lossless", err)
	}
	encoded, err := json.Marshal(orders.RealizationReport{Evidence: &record})
	if err != nil || bytes.Contains(encoded, []byte("PRIVATE")) || bytes.Contains(encoded, []byte("Chunks")) {
		t.Fatal("raw evidence escaped source DTO", err)
	}
	for _, chunk := range record.Chunks {
		if bytes.Contains(chunk, []byte("PRIVATE_FINANCIAL_HEADER")) {
			t.Fatal("plaintext persisted in ciphertext")
		}
	}
	cases := []struct {
		name   string
		change func(*orders.FinancialEvidenceRecord)
	}{
		{"tenant", func(r *orders.FinancialEvidenceRecord) { r.Scope.Tenant = "foreign" }},
		{"seller", func(r *orders.FinancialEvidenceRecord) { r.Scope.OzonAccount = "foreign" }},
		{"nativeaccount", func(r *orders.FinancialEvidenceRecord) { r.Scope.MoyskladAccount = "foreign" }},
		{"period", func(r *orders.FinancialEvidenceRecord) { r.Period.Month = 9 }},
		{"credential", func(r *orders.FinancialEvidenceRecord) { r.CredentialRevision++ }},
		{"sourceversion", func(r *orders.FinancialEvidenceRecord) {
			r.SourceFingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{"rawidentity", func(r *orders.FinancialEvidenceRecord) {
			r.RawDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{"cipher", func(r *orders.FinancialEvidenceRecord) { r.Chunks[0][0] ^= 1 }},
		{"chunkorder", func(r *orders.FinancialEvidenceRecord) { r.Chunks[0], r.Chunks[1] = r.Chunks[1], r.Chunks[0] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			changed := record
			changed.Chunks = make([][]byte, len(record.Chunks))
			for i, chunk := range record.Chunks {
				changed.Chunks[i] = bytes.Clone(chunk)
			}
			tc.change(&changed)
			if _, err := p.OpenFinancialEvidence(context.Background(), changed); err == nil {
				t.Fatal("substituted evidence decrypted")
			}
		})
	}
}
