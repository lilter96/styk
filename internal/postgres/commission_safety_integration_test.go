//go:build integration

package postgres

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

type commissionPGFixture struct {
	orderInventoryFixture
	preview orders.CommissionPreview
	work    orders.CommissionWork
	actor   string
}

func commissionPG(t *testing.T) commissionPGFixture {
	t.Helper()
	f := orderInventoryDBFixture(t)
	actor, found := identity.SessionFromContext(f.ctx)
	if !found {
		t.Fatal("owner missing")
	}
	id := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	f.config.Finance = &orders.CommissionPolicy{Enabled: true, OrganizationID: id(101), AgentID: id(102), ContractID: id(103), CurrencyID: id(104), CurrencyISO: "RUB", RateValue: "1", PriceBasis: orders.CommissionSellerPrice, VATEnabled: orders.Present(false), VATIncluded: orders.Present(false), VAT: "0", OtherComponents: orders.CommissionZeroOtherComponents}
	enabled := true
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: actor.AuditID, IdempotencyKey: "finance-policy", FinanceConsent: true, ReadEnabled: &enabled}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p := f.config.Finance
	now := time.Now().UTC()
	refs := orders.CommissionReferences{Scope: p.Scope, OrganizationID: p.OrganizationID, AgentID: p.AgentID, ContractID: p.ContractID, CurrencyID: p.CurrencyID, CurrencyISO: p.CurrencyISO, ContractType: "Commission", ContractOrganizationID: p.OrganizationID, ContractAgentID: p.AgentID, ContractCurrencyID: p.CurrencyID, ContractRewardType: "None", Revision: "verified-refs", ObservedAt: now, Complete: true}
	source := commissionPGSource(t, f.config.Scope, now)
	mappings, err := readOrderProductMappings(f.ctx, f.db.Pool, f.config.Scope)
	if err != nil {
		t.Fatal(err)
	}
	plan, issues := ordermoney.PlanCommissionDraft(f.config, orders.CredentialRevisions{Ozon: 1, Moysklad: 1}, source, mappings, refs, "commission-pg-preview", now)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	preview, err := f.db.SaveCommissionPreview(f.ctx, orders.CommissionPreview{ID: plan.ID, Plan: plan, Actor: actor.AuditID})
	if err != nil {
		t.Fatal(err)
	}
	work, err := f.db.ApproveCommissionPreview(f.ctx, orders.CommissionApproval{Scope: f.config.Scope, PreviewID: preview.ID, Actor: actor.AuditID, IdempotencyKey: "commission-approve"}, now)
	if err != nil {
		t.Fatal(err)
	}
	return commissionPGFixture{orderInventoryFixture: f, preview: preview, work: work, actor: actor.AuditID}
}

func commissionPGSource(t *testing.T, scope orders.Scope, now time.Time) orders.RealizationReport {
	t.Helper()
	zero := orders.Present(orders.Decimal("0"))
	returns := orders.RealizationAmounts{Presence: orders.PresenceValue, Quantity: zero, Amount: zero, Bonus: zero, StandardFee: zero, BankCoinvestment: zero, PickUpPointCoinvestment: zero, Stars: zero, Total: zero}
	delivery := returns
	delivery.Quantity, delivery.Amount, delivery.StandardFee, delivery.Total, delivery.PricePerInstance = orders.Present(orders.Decimal("2")), orders.Present(orders.Decimal("200")), orders.Present(orders.Decimal("20")), orders.Present(orders.Decimal("180")), orders.Present(orders.Decimal("100"))
	start := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	source := orders.RealizationReport{Scope: scope, Period: orders.RealizationPeriod{Year: start.Year(), Month: int(start.Month())}, Number: "COM-1", Currency: "RUB", DocumentDate: orders.Present(now.Format("2006-01-02")), StartDate: orders.Present(start.Format("2006-01-02")), StopDate: orders.Present(start.AddDate(0, 1, -1).Format("2006-01-02")), ObservedAt: now, Rows: []orders.RealizationRow{{Number: 1, SKU: orders.Present("2001"), OfferID: orders.Present("inventory-offer"), SellerUnitPrice: orders.Present(orders.Decimal("100")), Delivery: delivery, Return: returns}}}
	var err error
	source.Fingerprint, err = ordermoney.CommissionSourceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func (f *commissionPGFixture) claim(t *testing.T) orders.CommissionWork {
	t.Helper()
	now := time.Now()
	work, err := f.db.ClaimCommissionWork(f.ctx, orders.ClaimRequest{WorkerID: "finance-pg-worker", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || work == nil {
		t.Fatal("claim unavailable", err)
	}
	return *work
}

func commissionPGObserved(plan orders.CommissionPlan) orders.CommissionObservation {
	line := plan.Lines[0]
	return orders.CommissionObservation{Scope: plan.Scope, DocumentID: "native-report-id", ExternalCode: plan.ExternalCode, References: plan.References, RateValue: plan.Policy.RateValue, Moment: plan.Moment(), RewardType: "None", PeriodStart: plan.PeriodStart(), PeriodEnd: plan.PeriodEnd(), Gross: orders.Money{Amount: plan.Totals.Gross, Currency: plan.Totals.Currency}, Net: orders.Money{Amount: plan.Totals.Net, Currency: plan.Totals.Currency}, Lines: []orders.CommissionObservedLine{{ID: "native-line-id", ItemKind: line.Mapping.MoyskladKind, ItemID: line.Mapping.MoyskladID, Quantity: line.Quantity, Price: line.Price, Reward: line.Reward, VAT: plan.Policy.VAT, VATEnabled: *plan.Policy.VATEnabled.Value}}, VATEnabled: *plan.Policy.VATEnabled.Value, VATIncluded: *plan.Policy.VATIncluded.Value, Revision: "native-read-version", ObservedAt: time.Now().UTC(), Complete: true}
}

func TestCommissionPostgresFinalGuardRechecksConsentAndClaim(t *testing.T) {
	f := commissionPG(t)
	work := f.claim(t)
	allowed, err := f.db.GuardCommissionDispatch(f.ctx, work, false)
	if err != nil || !allowed {
		t.Fatal("initial current guard", allowed, err)
	}
	stale := work
	stale.ClaimToken = "stale-token"
	if _, err = f.db.GuardCommissionDispatch(f.ctx, stale, true); !errors.Is(err, orders.ErrStale) {
		t.Fatal("stale claimant authorized", err)
	}
	actual := commissionPGObserved(work.Plan)
	err = f.db.CompleteCommissionWork(f.ctx, orders.CommissionCompletion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: orders.StateClaimed, State: orders.StateReconciled, Observation: &actual})
	if !errors.Is(err, orders.ErrStale) {
		t.Fatal("zero-dispatch work falsely reconciled", err)
	}
	f.config.Finance.Enabled = false
	_, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.actor, IdempotencyKey: "finance-revoke-final"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	allowed, err = f.db.GuardCommissionDispatch(f.ctx, work, true)
	if err != nil || allowed {
		t.Fatal("revoked consent passed final admission", allowed, err)
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT dispatch_count FROM orders_finance_work WHERE id=$1`, work.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("denied guard recorded dispatch", count, err)
	}
}

func TestCommissionPostgresUnknownIsReadOnlyAndTerminalEvidenceImmutable(t *testing.T) {
	f := commissionPG(t)
	work := f.claim(t)
	allowed, err := f.db.GuardCommissionDispatch(f.ctx, work, true)
	if err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	receipt := orders.Receipt{CommandID: work.Plan.ID, State: orders.StateUnknown, Code: "TRANSPORT_UNCERTAIN", ObservedAt: time.Now().UTC()}
	err = f.db.CompleteCommissionWork(f.ctx, orders.CommissionCompletion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StateUnknown, Receipt: receipt, Code: "TRANSPORT_UNCERTAIN"})
	if err != nil {
		t.Fatal(err)
	}
	read := f.claim(t)
	if read.State != orders.StateUnknown || read.DispatchCount != 1 {
		t.Fatal("unknown work reset", read.State, read.DispatchCount)
	}
	allowed, err = f.db.GuardCommissionDispatch(f.ctx, read, false)
	if err != nil || !allowed {
		t.Fatal("unknown readback denied", allowed, err)
	}
	allowed, err = f.db.GuardCommissionDispatch(f.ctx, read, true)
	if err != nil || allowed {
		t.Fatal("unknown mutation admitted", allowed, err)
	}
	_, err = f.db.Pool.Exec(f.ctx, `UPDATE orders_finance_work SET state='QUEUED',work=jsonb_set(work,'{state}','"QUEUED"') WHERE id=$1`, work.ID)
	merchantJournalSQLState(t, err, "P0001")
	actual := commissionPGObserved(work.Plan)
	err = f.db.CompleteCommissionWork(f.ctx, orders.CommissionCompletion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: orders.StateUnknown, State: orders.StateReconciled, Observation: &actual})
	if !errors.Is(err, orders.ErrStale) {
		t.Fatal("old claim completed recovered work", err)
	}
	err = f.db.CompleteCommissionWork(f.ctx, orders.CommissionCompletion{Scope: read.Scope, WorkID: read.ID, ClaimToken: read.ClaimToken, ExpectedState: orders.StateUnknown, State: orders.StateReconciled, Observation: &actual})
	if err != nil {
		t.Fatal("exact independent readback unavailable", err)
	}
	_, err = f.db.Pool.Exec(f.ctx, `UPDATE orders_finance_work SET work=jsonb_set(work,'{code}','"MUTATED"') WHERE id=$1`, work.ID)
	merchantJournalSQLState(t, err, "P0001")
	history, err := f.db.CommissionHistory(f.ctx, work.Scope, 10)
	if err != nil || len(history) != 1 || history[0].Receipt.State != orders.StateUnknown || history[0].DocumentID != actual.DocumentID {
		t.Fatal("original uncertainty or native evidence lost", history, err)
	}
}

func TestCommissionPostgresScopesPlanAndApprovalRollback(t *testing.T) {
	f := commissionPG(t)
	work := f.claim(t)
	foreign := work
	foreign.Scope.OzonAccount = "foreign"
	if _, err := f.db.GuardCommissionDispatch(f.ctx, foreign, true); !errors.Is(err, orders.ErrNotFound) {
		t.Fatal("cross-scope claim authorized", err)
	}
	history, err := f.db.CommissionHistory(f.ctx, foreign.Scope, 10)
	if err != nil || len(history) != 0 {
		t.Fatal("cross-scope history leaked", err)
	}
	_, err = f.db.Pool.Exec(f.ctx, `UPDATE orders_finance_work SET work=jsonb_set(work,'{plan,totals,net}','"1"') WHERE id=$1`, work.ID)
	merchantJournalSQLState(t, err, "P0001")
	_, err = f.db.ApproveCommissionPreview(f.ctx, orders.CommissionApproval{Scope: f.config.Scope, PreviewID: f.preview.ID, Actor: f.actor, IdempotencyKey: "another-approval"}, time.Now())
	if !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("period duplicate approved", err)
	}
	var works, tasks int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_finance_work`).Scan(&works); err != nil {
		t.Fatal(err)
	}
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM outbox_tasks WHERE task_type='orders_finance'`).Scan(&tasks); err != nil || works != 1 || tasks != 1 {
		t.Fatal("failed approval partially committed", works, tasks, err)
	}
}

func TestCommissionPostgresMigrationRefusesPolicyOrReportHistory(t *testing.T) {
	f := commissionPG(t)
	sections := nativePointMigrationSections(t, "00073_orders_commission_drafts.sql")
	_, err := f.db.Pool.Exec(f.ctx, sections[1])
	merchantJournalSQLState(t, err, "P0001")
	history, err := f.db.CommissionHistory(f.ctx, f.config.Scope, 10)
	if err != nil || len(history) != 1 {
		t.Fatal("refused rollback removed finance evidence", err)
	}
}

func TestCommissionPostgresMigrationEmptyRollbackAndUpgrade(t *testing.T) {
	f := orderInventoryDBFixture(t)
	sections := nativePointMigrationSections(t, "00073_orders_commission_drafts.sql")
	ledger := nativePointMigrationSections(t, "00074_financial_component_ledgers.sql")
	carriage := nativePointMigrationSections(t, "00075_orders_carriage_batches.sql")
	archive := nativePointMigrationSections(t, "00076_financial_raw_evidence.sql")
	executeNativePointMigration(t, f, archive[1])
	executeNativePointMigration(t, f, carriage[1])
	var legacyPayload string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='outbox_tasks'::regclass AND conname='outbox_task_payload_check'`).Scan(&legacyPayload); err != nil {
		t.Fatal(err)
	}
	executeNativePointMigration(t, f, ledger[1])
	executeNativePointMigration(t, f, sections[1])
	var rolledBackPayload string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='outbox_tasks'::regclass AND conname='outbox_task_payload_check'`).Scan(&rolledBackPayload); err != nil || rolledBackPayload != legacyPayload {
		t.Fatal("finance downgrade removed or changed restored prior payload guard", err)
	}
	executeNativePointMigration(t, f, sections[0])
	executeNativePointMigration(t, f, ledger[0])
	executeNativePointMigration(t, f, carriage[0])
	executeNativePointMigration(t, f, archive[0])
	var exists bool
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT to_regclass('orders_finance_work') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatal("finance roundtrip failed", err)
	}
	var afterPayload string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='outbox_tasks'::regclass AND conname='outbox_task_payload_check'`).Scan(&afterPayload); err != nil || afterPayload == "" {
		t.Fatal("staged upgrade did not restore payload guard", err)
	}
}

func TestCommissionPostgresReadbackRetainsAcknowledgedNativeIdentity(t *testing.T) {
	f := commissionPG(t)
	work := f.claim(t)
	allowed, err := f.db.GuardCommissionDispatch(f.ctx, work, true)
	if err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	receipt := orders.Receipt{CommandID: work.Plan.ID, State: orders.StateAcknowledged, ExternalID: "native-report-id", ObservedAt: time.Now().UTC()}
	err = f.db.CompleteCommissionWork(f.ctx, orders.CommissionCompletion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StateAcknowledged, Receipt: receipt})
	if err != nil {
		t.Fatal(err)
	}
	read := f.claim(t)
	actual := commissionPGObserved(work.Plan)
	actual.DocumentID = "foreign-native-report"
	completion := orders.CommissionCompletion{Scope: read.Scope, WorkID: read.ID, ClaimToken: read.ClaimToken, ExpectedState: orders.StateAcknowledged, State: orders.StateReconciled, Observation: &actual}
	if err = f.db.CompleteCommissionWork(f.ctx, completion); !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("different native identity reconciled", err)
	}
	actual.DocumentID = receipt.ExternalID
	if err = f.db.CompleteCommissionWork(f.ctx, completion); err != nil {
		t.Fatal("exact acknowledged native identity rejected", err)
	}
}
