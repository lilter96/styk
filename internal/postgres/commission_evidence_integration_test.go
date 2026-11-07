//go:build integration

package postgres

import (
	"bytes"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
)

func TestFinancialRawEvidencePostgresRetainsDistinctRawVersionsAndDecrypts(t *testing.T) {
	f := commissionPG(t)
	vault, err := credentials.New(bytes.Repeat([]byte{91}, 32))
	if err != nil {
		t.Fatal(err)
	}
	protector := orderprivacy.FinancialEvidenceProtector{Vault: vault}
	source := f.preview.Plan.Source
	for _, raw := range [][]byte{[]byte(`{"private_header":"PRIVATE_A","exact":9007199254740993.01}`), []byte(`{"private_header":"PRIVATE_B","exact":9007199254740993.01}`)} {
		record, err := protector.SealFinancialEvidence(f.ctx, source.Scope, source.Period, 1, raw, source.Fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		source.Evidence = &record
		if err = f.db.ObserveCommissionReport(f.ctx, source); err != nil {
			t.Fatal(err)
		}
		stored, err := f.db.FinancialEvidence(f.ctx, source.Scope, source.Period, 1, source.Fingerprint, record.RawDigest)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := protector.OpenFinancialEvidence(f.ctx, stored)
		if err != nil || !bytes.Equal(decoded, raw) {
			t.Fatal("stored source not independently decryptable", err)
		}
		if err = f.db.ObserveCommissionReport(f.ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	var records, sources, ledgers int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM orders_finance_raw_evidence),(SELECT count(*) FROM orders_finance_observations),(SELECT count(*) FROM orders_finance_ledgers)`).Scan(&records, &sources, &ledgers); err != nil || records != 2 || sources != 1 || ledgers != 1 {
		t.Fatal("raw/financial identities mixed", records, sources, ledgers, err)
	}
	_, err = f.db.Pool.Exec(f.ctx, `UPDATE orders_finance_raw_evidence SET body_bytes=body_bytes+1`)
	merchantJournalSQLState(t, err, "55000")
	sections := nativePointMigrationSections(t, "00076_financial_raw_evidence.sql")
	_, err = f.db.Pool.Exec(f.ctx, sections[1])
	merchantJournalSQLState(t, err, "P0001")
}

func TestFinancialRawEvidencePostgresRejectsForeignScopeAndCredentialRevision(t *testing.T) {
	f := commissionPG(t)
	vault, err := credentials.New(bytes.Repeat([]byte{92}, 32))
	if err != nil {
		t.Fatal(err)
	}
	p := orderprivacy.FinancialEvidenceProtector{Vault: vault}
	source := f.preview.Plan.Source
	record, err := p.SealFinancialEvidence(f.ctx, source.Scope, source.Period, 2, []byte(`{"private":"no-current-credential"}`), source.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	source.Evidence = &record
	if err = f.db.ObserveCommissionReport(f.ctx, source); !errors.Is(err, orders.ErrStale) {
		t.Fatal("wrong credential archived", err)
	}
	record.CredentialRevision = 1
	record.Scope.OzonAccount = "foreign"
	if err = f.db.ObserveCommissionReport(f.ctx, source); err == nil {
		t.Fatal("foreign raw evidence archived")
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_finance_raw_evidence`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected archive partially committed", count, err)
	}
}
