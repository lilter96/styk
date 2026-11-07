//go:build integration

package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func TestFinancialLedgerPostgresIsScopedImmutableAndDeduplicated(t *testing.T) {
	f := commissionPG(t)
	source := f.preview.Plan.Source
	source.ObservedAt = time.Now().UTC()
	if err := f.db.ObserveCommissionReport(f.ctx, source); err != nil {
		t.Fatal(err)
	}
	var count, rows, components int
	err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*),max((ledger->>'rows_classified')::int),max((ledger->>'components_total')::int) FROM orders_finance_ledgers`).Scan(&count, &rows, &components)
	if err != nil || count != 1 || rows != 1 || components != 24 {
		t.Fatal("ledger coverage or dedup failed", count, rows, components, err)
	}
	_, err = f.db.Pool.Exec(f.ctx, `UPDATE orders_finance_ledgers SET ledger=jsonb_set(ledger,'{complete}','true')`)
	merchantJournalSQLState(t, err, "55000")
	_, err = f.db.Pool.Exec(f.ctx, `DELETE FROM orders_finance_ledgers`)
	merchantJournalSQLState(t, err, "55000")
	_, err = f.db.Pool.Exec(f.ctx, `INSERT INTO orders_finance_ledgers(tenant_id,moysklad_account,ozon_account,report_year,report_month,source_fingerprint,ledger_version,ledger) SELECT tenant_id,moysklad_account,'foreign-account',report_year,report_month,source_fingerprint,ledger_version,ledger FROM orders_finance_ledgers`)
	merchantJournalSQLState(t, err, "23503")
	sections := nativePointMigrationSections(t, "00074_financial_component_ledgers.sql")
	_, err = f.db.Pool.Exec(f.ctx, sections[1])
	merchantJournalSQLState(t, err, "P0001")
}

func TestFinancialLedgerPostgresFailureRollsBackSourceAndPeriodHead(t *testing.T) {
	f := commissionPG(t)
	_, err := f.db.Pool.Exec(f.ctx, `CREATE FUNCTION reject_test_ledger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected ledger insert failure'; END $$; CREATE TRIGGER reject_test_ledger BEFORE INSERT ON orders_finance_ledgers FOR EACH ROW EXECUTE FUNCTION reject_test_ledger()`)
	if err != nil {
		t.Fatal(err)
	}
	source := f.preview.Plan.Source
	source.Number += "-amended"
	source.ObservedAt = time.Now().UTC()
	source.Fingerprint, err = ordermoney.CommissionSourceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.db.ObserveCommissionReport(f.ctx, source); err == nil {
		t.Fatal("failed ledger partially committed source")
	}
	var observations, ledgers int
	var head string
	var changed bool
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM orders_finance_observations),(SELECT count(*) FROM orders_finance_ledgers),current_fingerprint,source_changed FROM orders_finance_periods`).Scan(&observations, &ledgers, &head, &changed); err != nil {
		t.Fatal(err)
	}
	if observations != 1 || ledgers != 1 || head != f.preview.Plan.Source.Fingerprint || changed {
		t.Fatal("source/ledger/head did not roll back", observations, ledgers, head, changed)
	}
}

func TestFinancialLedgerPostgresPolicyOffDiagnosticCannotEnrollWork(t *testing.T) {
	f := commissionPG(t)
	f.config.Finance.Enabled = false
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.actor, IdempotencyKey: "disable-finance-for-diagnostic"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source := f.preview.Plan.Source
	source.ObservedAt = now
	plan, validation := ordermoney.PlanCommissionDraft(f.config, f.work.Credentials, source, nil, orders.CommissionReferences{}, "policy-off-diagnostic", now)
	analysis, err := ordermoney.ClassifyRealization(source)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := f.db.SaveCommissionPreview(f.ctx, orders.CommissionPreview{ID: plan.ID, Plan: plan, Analysis: &analysis, Validation: validation, Actor: f.actor})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.db.ApproveCommissionPreview(f.ctx, orders.CommissionApproval{Scope: source.Scope, PreviewID: preview.ID, Actor: f.actor, IdempotencyKey: "blocked-diagnostic-approval"}, now)
	if !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("policy-off diagnostic gained accounting authority", err)
	}
	commissionRetryAssertHead(t, f, f.work.ID, 1)
}
