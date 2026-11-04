//go:build integration

package integration

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersHybridLabelProofIsScopedAndRequiredAtFinalDispatch(t *testing.T) {
	f := newLabelStoreFixture(t)
	label := finishHybridLabelFixture(t, f)
	proof, err := f.db.OrderLabelProof(f.ctx, f.config.Scope, label.Command.PostingNumber, label.ID)
	if err != nil || proof.WorkID != label.ID || proof.TaskID != "99" {
		t.Fatalf("stored label proof missing: %v", err)
	}
	assertHybridLabelScope(t, f, label)
	p := hybridShipPreview(t, f, label)
	ordersStoreApprove(t, f, p)
	w := ordersStoreClaim(t, f, time.Minute)
	allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: w, Now: time.Now()})
	if err != nil || !allowed {
		t.Fatalf("valid label prerequisite rejected: %v", err)
	}
	// Retention cleanup can remove the PDF after approval but before admission.
	if _, err = f.db.Pool.Exec(f.ctx, `DELETE FROM orders_label_artifacts WHERE work_id=$1`, label.ID); err != nil {
		t.Fatal(err)
	}
	allowed, err = f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: w, Begin: true, Now: time.Now()})
	if err != nil || allowed {
		t.Fatalf("removed label allowed dispatch: allowed=%v error=%v", allowed, err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_write_intents`, 1)
}

func TestOrdersHybridLabelRemovedBetweenPreviewAndApprovalRejectsApproval(t *testing.T) {
	f := newLabelStoreFixture(t)
	label := finishHybridLabelFixture(t, f)
	p := hybridShipPreview(t, f, label)
	if _, err := f.db.Pool.Exec(f.ctx, `DELETE FROM orders_label_artifacts WHERE work_id=$1`, label.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.db.ApproveOrderPreview(f.ctx, orders.Approval{Scope: f.config.Scope, PreviewID: p.ID, Actor: f.session.AuditID, IdempotencyKey: "hybrid-stale-proof"}, time.Now())
	if !errors.Is(err, orders.ErrStale) {
		t.Fatalf("missing label approval: %v", err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work`, 1)
}

func finishHybridLabelFixture(t *testing.T, f ordersStoreFixture) orders.Work {
	t.Helper()
	w, receipt := hybridLabelStoreWork(t, f)
	vault, err := credentials.New(bytes.Repeat([]byte{33}, 32))
	if err != nil {
		t.Fatal(err)
	}
	a, err := (orderprivacy.LabelProtector{Vault: vault}).SealOrderLabel(w, receipt.TaskIDs[0], []byte("%PDF-1.7\nSYNTHETIC\n%%EOF"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	a.ExpiresAt = a.CreatedAt.Add(time.Hour)
	receipt.State = orders.StateReconciled
	receipt.LabelTask = &orders.LabelTask{ID: "99", Code: "completed", CountsKnown: true, PostingsCount: 1, PrintedPostingsCount: 1}
	receipt.Artifact = &orders.OrderArtifact{Available: true, DownloadURL: orders.LabelArtifactURL(w.ID), LabelType: "big_label", ExpiresAt: a.ExpiresAt}
	c := orders.Completion{WorkID: w.ID, Scope: w.Scope, ClaimToken: w.ClaimToken, ExpectedState: w.State, State: orders.StateReconciled, Receipt: receipt, LabelArtifact: &a}
	if err = f.db.CompleteOrderWork(f.ctx, c); err != nil {
		t.Fatal(err)
	}
	return w
}

func assertHybridLabelScope(t *testing.T, f ordersStoreFixture, label orders.Work) {
	t.Helper()
	for _, mutate := range []func(*orders.Scope){
		func(s *orders.Scope) { s.Tenant = "other" },
		func(s *orders.Scope) { s.OzonAccount = "other" },
		func(s *orders.Scope) { s.MoyskladAccount = "other" },
	} {
		scope := f.config.Scope
		mutate(&scope)
		if _, err := f.db.OrderLabelProof(f.ctx, scope, label.Command.PostingNumber, label.ID); !errors.Is(err, orders.ErrNotFound) {
			t.Fatal("label proof crossed account boundary")
		}
	}
	if _, err := f.db.OrderLabelProof(f.ctx, f.config.Scope, "other-posting", label.ID); !errors.Is(err, orders.ErrNotFound) {
		t.Fatal("label proof crossed posting boundary")
	}
}

func hybridShipPreview(t *testing.T, f ordersStoreFixture, label orders.Work) orders.Preview {
	t.Helper()
	p, err := f.db.SaveOrderPreview(f.ctx, hybridShipDraft(t, f, label))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func hybridShipDraft(t *testing.T, f ordersStoreFixture, label orders.Work) orders.Preview {
	t.Helper()
	current, err := f.db.OrderReviewContext(f.ctx, f.config.Scope)
	if err != nil || len(current.Postings) != 1 {
		t.Fatalf("read hybrid source fixture: %v", err)
	}
	posting := current.Postings[0]

	p := ordersStorePreview(t, f, posting.PostingNumber)
	p.ID += "-hybrid"
	c := &p.Decisions[0].Commands[0]
	c.ID += "-hybrid"
	c.IdempotencyKey = c.ID
	c.Action, c.CustomerOrder = orders.ActionPostingShip, nil
	c.Warehouse.Capability = label.Command.Warehouse.Capability
	c.PostingAction = &orders.PostingAction{Capability: c.Warehouse.Capability, ContractHash: orderactions.ExecutionContractHash(), Parameters: orders.ActionParameters{EventConfirmed: true, EventReferenceProvided: true, LabelPrintedConfirmed: true, LabelWorkID: label.ID}}
	return p
}

func hybridLabelStoreWork(t *testing.T, f ordersStoreFixture) (orders.Work, orders.Receipt) {
	t.Helper()
	posting := ordersStorePosting(f.config.Scope, "hybrid-label-posting", "hybrid-source-v1", time.Now().UTC())
	posting.Delivery.IntegrationType = orders.Present("hybrid")
	ordersStoreCommit(t, f, posting, "hybrid-initial-source")
	p := ordersStorePreview(t, f, posting.PostingNumber)
	p.ID += "-label"
	c := &p.Decisions[0].Commands[0]
	c.ID += "-label"
	c.IdempotencyKey = c.ID
	c.Action, c.CustomerOrder = orders.ActionPostingLabelCreate, nil
	cap := orders.CapabilityRef{ID: "synthetic-label-capability", Revision: 1, WarehouseID: *posting.WarehouseID.Value, DeliveryMethodID: *posting.DeliveryMethodID.Value, Scheme: "FBS", ObservedAt: time.Now().UTC(), ValidUntil: time.Now().UTC().Add(time.Hour)}
	c.Warehouse.Capability = cap
	c.PostingAction = &orders.PostingAction{Capability: cap, ContractHash: orderactions.ExecutionContractHash(), Parameters: orders.ActionParameters{LabelType: "big_label"}}
	p, err := f.db.SaveOrderPreview(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreApprove(t, f, p)
	w := ordersStoreClaim(t, f, time.Minute)
	allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: w, Begin: true, Now: time.Now()})
	if err != nil || !allowed {
		t.Fatalf("label dispatch guard: %v", err)
	}
	receipt := orders.Receipt{CommandID: w.Command.ID, State: orders.StatePolling, TaskID: "99", TaskIDs: []orders.ActionTask{{ID: "99", Type: "big_label"}}, LabelTasksVerified: true, HTTPStatus: 200, ObservedAt: time.Now().UTC()}
	cpl := orders.Completion{WorkID: w.ID, Scope: w.Scope, ClaimToken: w.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StatePolling, Receipt: receipt}
	if err = f.db.CompleteOrderWork(f.ctx, cpl); err != nil {
		t.Fatal(err)
	}
	return ordersStoreClaim(t, f, time.Minute), receipt
}

func TestOrdersHybridOldLabelCannotAuthorizeNewComposition(t *testing.T) {
	f := newLabelStoreFixture(t)
	label := finishHybridLabelFixture(t, f)
	current, err := f.db.OrderReviewContext(f.ctx, f.config.Scope)
	if err != nil || len(current.Postings) != 1 {
		t.Fatal("missing hybrid source")
	}
	posting := current.Postings[0]
	posting.Lines = append([]orders.PostingLine(nil), posting.Lines...)
	posting.Lines[0].Quantity = "1"
	posting.Evidence.Revision = "hybrid-source-v2"
	posting.Evidence.ProviderUpdatedAt = orders.Present(time.Now().UTC())
	ordersStoreCommit(t, f, posting, "hybrid-source-changed")
	proof, err := f.db.OrderLabelProof(f.ctx, f.config.Scope, posting.PostingNumber, label.ID)
	if err != nil {
		t.Fatal(err)
	}
	if proof.SourceRevision != label.Command.ExpectedSourceRevision || !errors.Is(proof.ValidateFor(posting, label.ID, time.Now()), orders.ErrStale) {
		t.Fatal("old label authorized changed posting composition")
	}
	if _, err = f.db.SaveOrderPreview(f.ctx, hybridShipDraft(t, f, label)); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("new composition accepted old printed label at preview: %v", err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work`, 1)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_write_intents`, 1)

}
