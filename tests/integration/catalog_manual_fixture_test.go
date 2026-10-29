//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/postgres"
)

func catalogManualUnknown(t *testing.T) (*postgres.Store, catalogsync.Work) {
	t.Helper()
	return catalogManualUnknownReceipt(t, nil)
}

func catalogManualUnknownReceipt(t *testing.T, change func(*catalog.Receipt)) (*postgres.Store, catalogsync.Work) {
	t.Helper()
	db, config := catalogSyncFixture(t)
	catalogSyncApprove(t, db, catalogSyncPreview(t, db, config))
	work := catalogSyncClaim(t, db)
	if ok, err := db.GuardCatalogDispatch(t.Context(), catalogsync.DispatchGuard{Work: work, Now: time.Now(), Begin: true}); err != nil || !ok {
		t.Fatalf("journal initial dispatch: allowed=%v error=%v", ok, err)
	}
	receipt := catalogManualReceipt(work)
	if change != nil {
		change(&receipt)
	}
	err := db.CompleteCatalogWork(t.Context(), catalogsync.Completion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: catalogsync.StateDispatching, State: catalogsync.StateUnknown, Code: "PROVIDER_OUTCOME_UNKNOWN", Receipt: receipt, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return db, catalogManualHistory(t, db, work.Scope)
}

func catalogManualReceipt(work catalogsync.Work) catalog.Receipt {
	warning := catalog.FieldError{Field: catalog.FieldAttributes, Code: "warning_attribute_values_empty", Severity: "warning", AttributeID: 85}
	return catalog.Receipt{CommandID: work.Command.ID, State: catalog.ReceiptPartial, HTTPStatus: 200, TaskID: "synthetic-known-task", ExternalID: work.Command.Target.ID, Errors: []catalog.FieldError{warning}, Products: []catalog.ProductReceipt{{ID: work.Command.Target.ID, OfferID: work.Command.Target.OfferID, Status: "imported", Errors: []catalog.FieldError{warning}}}}
}

func catalogManualHistory(t *testing.T, db *postgres.Store, scope catalog.Scope) catalogsync.Work {
	t.Helper()
	history, err := db.CatalogHistory(t.Context(), scope, catalogsync.PageRequest{Limit: 10})
	if err != nil || len(history.Operations) != 1 || len(history.Operations[0].Targets) != 1 {
		t.Fatalf("read isolated history: %+v %v", history, err)
	}
	return history.Operations[0].Targets[0]
}

func catalogManualRequest(work catalogsync.Work, key string) catalogsync.ReconciliationRequest {
	return catalogsync.ReconciliationRequest{Scope: work.Scope, WorkID: work.ID, Actor: "catalog-test-actor", Reason: "Review known synthetic provider task", IdempotencyKey: key, Acknowledge: true, AcceptWarnings: true}
}

func catalogManualBegin(t *testing.T, db *postgres.Store, work catalogsync.Work, key string) catalogsync.ReconciliationClaim {
	t.Helper()
	claim, err := db.BeginCatalogReconciliation(t.Context(), catalogManualRequest(work, key), time.Now())
	if err != nil || claim.ID == "" || claim.Token == "" || claim.Result != nil {
		t.Fatalf("begin unknown readback: claim=%+v error=%v", claim, err)
	}
	return claim
}

func catalogManualCompletion(claim catalogsync.ReconciliationClaim) catalogsync.Completion {
	return catalogsync.Completion{WorkID: claim.Work.ID, Scope: claim.Work.Scope, ClaimToken: claim.Token, ExpectedState: catalogsync.StateUnknown, State: catalogsync.StateUnknown, Code: "TASK_DIAGNOSTICS_REQUIRE_REVIEW", Receipt: claim.Work.Receipt, Now: time.Now()}
}

type catalogManualFrozen struct {
	outbox, command, unknownAttempt, receipt string
	receiptLimit, attemptLimit               int64
}

func catalogManualFreeze(t *testing.T, db *postgres.Store, work catalogsync.Work) catalogManualFrozen {
	t.Helper()
	var frozen catalogManualFrozen
	err := db.Pool.QueryRow(t.Context(), `SELECT (SELECT to_jsonb(q)::text FROM outbox_tasks q WHERE q.catalog_work_id=$1), (SELECT command::text FROM catalog_sync_commands WHERE work_id=$1), (SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id)::text FROM catalog_sync_attempts a WHERE work_id=$1 AND phase='UNKNOWN'), (SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id)::text FROM catalog_sync_receipts r WHERE work_id=$1), (SELECT max(id) FROM catalog_sync_receipts WHERE work_id=$1), (SELECT max(id) FROM catalog_sync_attempts WHERE work_id=$1)`, work.ID).Scan(&frozen.outbox, &frozen.command, &frozen.unknownAttempt, &frozen.receipt, &frozen.receiptLimit, &frozen.attemptLimit)
	if err != nil {
		t.Fatal(err)
	}
	return frozen
}

func catalogManualAssertFrozen(t *testing.T, db *postgres.Store, work catalogsync.Work, before catalogManualFrozen) {
	t.Helper()
	after := catalogManualFreeze(t, db, work)
	if before.outbox != after.outbox || before.command != after.command {
		t.Fatal("manual readback rewrote original failed outbox, approved command, or UNKNOWN attempt")
	}
	var originalAttempts string
	if err := db.Pool.QueryRow(t.Context(), `SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id)::text FROM catalog_sync_attempts a WHERE work_id=$1 AND phase='UNKNOWN' AND id<=$2`, work.ID, before.attemptLimit).Scan(&originalAttempts); err != nil || originalAttempts != before.unknownAttempt {
		t.Fatalf("original UNKNOWN attempt history changed: %v", err)
	}
	var originalReceipts string
	if err := db.Pool.QueryRow(t.Context(), `SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id)::text FROM catalog_sync_receipts r WHERE work_id=$1 AND id<=$2`, work.ID, before.receiptLimit).Scan(&originalReceipts); err != nil || originalReceipts != before.receipt {
		t.Fatalf("original provider receipt history changed: %v", err)
	}
	var state string
	var dispatches int
	if err := db.Pool.QueryRow(t.Context(), `SELECT q.state,w.dispatch_count FROM outbox_tasks q JOIN catalog_sync_work w ON w.id=q.catalog_work_id WHERE w.id=$1`, work.ID).Scan(&state, &dispatches); err != nil || state != "failed" || dispatches != 1 {
		t.Fatalf("original mutation task changed: state=%s dispatches=%d error=%v", state, dispatches, err)
	}
}

type catalogManualProvider struct {
	scope                catalog.Scope
	product              catalog.Product
	receipt              catalog.Receipt
	reads, polls, writes int
}

func (p *catalogManualProvider) ReadCatalog(_ context.Context, _ catalog.ReadRequest) (catalog.Snapshot, error) {
	p.reads++
	return catalog.Snapshot{Scope: p.scope, Account: p.product.Identity.Account, Products: []catalog.Product{p.product}, Complete: true, Revision: p.product.Revision, ObservedAt: time.Now()}, nil
}

func (p *catalogManualProvider) WriteCatalog(context.Context, catalog.Command) (catalog.Receipt, error) {
	p.writes++
	return catalog.Receipt{}, errors.New("manual readback must never write")
}

func (p *catalogManualProvider) BindCatalogIntent(catalog.Command, func(context.Context) (bool, error)) error {
	return errors.New("manual readback must never bind a write intent")
}

func (p *catalogManualProvider) PollCatalog(context.Context, catalog.Command, catalog.Receipt) (catalog.Receipt, error) {
	p.polls++
	return p.receipt, nil
}

func catalogManualReconciler(db *postgres.Store, work catalogsync.Work) (catalogsync.Reconciler, *catalogManualProvider, *catalogManualProvider) {
	source := &catalogManualProvider{scope: work.Scope, product: work.Decision.Source}
	target := &catalogManualProvider{scope: work.Scope, product: work.Decision.Target, receipt: catalogManualReceipt(work)}
	target.product.Fields = map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("New name")}
	target.product.Revision = "verified-synthetic-provider-revision"
	resolver := catalogsync.ResolveFunc(func(_ context.Context, scope catalog.Scope, account catalog.Account, revision int64) (catalogsync.Binding, error) {
		client := target
		if account.Provider == catalog.ProviderMoySklad {
			client = source
		}
		return catalogsync.Binding{Scope: scope, Account: account, CredentialRevision: revision, Client: client}, nil
	})
	return catalogsync.Reconciler{Store: db, Resolver: resolver, AllowScope: func(scope catalog.Scope) bool { return scope == work.Scope }}, source, target
}

func catalogManualJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type catalogManualClaimAnswer struct {
	claim catalogsync.ReconciliationClaim
	req   catalogsync.ReconciliationRequest
	err   error
}

func catalogManualConcurrentClaim(t *testing.T, db *postgres.Store, req catalogsync.ReconciliationRequest) (catalogsync.ReconciliationClaim, catalogsync.ReconciliationRequest) {
	t.Helper()
	answers := make(chan catalogManualClaimAnswer, 2)
	start := make(chan struct{})
	for _, suffix := range []string{"-first", "-second"} {
		go func() {
			<-start
			request := req
			request.IdempotencyKey += suffix
			claim, err := db.BeginCatalogReconciliation(t.Context(), request, time.Now())
			answers <- catalogManualClaimAnswer{claim, request, err}
		}()
	}
	close(start)
	var winner catalogManualClaimAnswer
	var successes, conflicts int
	for range 2 {
		got := <-answers
		if got.err == nil {
			successes++
			winner = got
			continue
		}
		if errors.Is(got.err, catalogsync.ErrConflict) {
			conflicts++
			continue
		}
		t.Fatalf("unexpected concurrent claim error: %v", got.err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("claims=%d conflicts=%d", successes, conflicts)
	}
	return winner.claim, winner.req
}
