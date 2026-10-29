package catalogsync

import (
	"context"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

// ReconciliationRequest authorizes reads of an already dispatched task. It does
// not authorize another external write or supersede an uncertain operation.
type ReconciliationRequest struct {
	Scope          catalog.Scope `json:"scope"`
	WorkID         string        `json:"work_id"`
	Actor          string        `json:"-"`
	Reason         string        `json:"reason"`
	IdempotencyKey string        `json:"idempotency_key"`
	Acknowledge    bool          `json:"acknowledge"`
	AcceptWarnings bool          `json:"accept_warnings"`
}

type ReconciliationResult struct {
	ID        string `json:"id"`
	WorkID    string `json:"work_id"`
	State     State  `json:"state"`
	Code      string `json:"code"`
	Completed bool   `json:"completed"`
}

type ReconciliationClaim struct {
	ID     string
	Token  string
	Work   Work
	Result *ReconciliationResult
}

type ReconciliationStore interface {
	BeginCatalogReconciliation(context.Context, ReconciliationRequest, time.Time) (ReconciliationClaim, error)
	CompleteCatalogReconciliation(context.Context, ReconciliationClaim, Completion) (ReconciliationResult, error)
}

type Reconciler struct {
	Store      ReconciliationStore
	Resolver   Resolver
	AllowScope func(catalog.Scope) bool
	Now        func() time.Time
}

func (r Reconciler) Reconcile(ctx context.Context, req ReconciliationRequest) (ReconciliationResult, error) {
	if r.Store == nil || r.Resolver == nil || req.Scope.Validate() != nil || !validID(req.WorkID) || !validID(req.Actor) || !validID(req.IdempotencyKey) || !req.Acknowledge || len(strings.TrimSpace(req.Reason)) < 5 || len(req.Reason) > 1000 {
		return ReconciliationResult{}, ErrInvalid
	}
	if r.AllowScope == nil || !r.AllowScope(req.Scope) {
		return ReconciliationResult{}, ErrBlocked
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	claim, err := r.Store.BeginCatalogReconciliation(ctx, req, now)
	if err != nil {
		return ReconciliationResult{}, err
	}
	if claim.Result != nil {
		return *claim.Result, nil
	}
	return r.checkClaim(ctx, req, claim, now)
}

func (r Reconciler) checkClaim(ctx context.Context, req ReconciliationRequest, claim ReconciliationClaim, now time.Time) (ReconciliationResult, error) {
	store := &readbackCompletionStore{store: r.Store, claim: claim}
	runner := Runner{Enabled: true, WorkerID: "manual-catalog-readback", Store: store, Resolver: r.Resolver, AllowScope: r.AllowScope, Now: r.Now}
	if claim.Work.State != StateUnknown || claim.Work.Scope != req.Scope || claim.Work.ID != req.WorkID || claim.Work.validate() != nil {
		return store.finish(ctx, Completion{State: StateUnknown, Code: "INVALID_READBACK_INTENT"}, now)
	}
	receipt, err := r.pollKnownTask(ctx, claim.Work)
	if err != nil || !ReconciliationReceiptPermitted(claim.Work.Command, receipt, req.AcceptWarnings) {
		if !validReceiptFor(claim.Work.Command, receipt) {
			receipt = claim.Work.Receipt
		}
		return store.finish(ctx, Completion{State: StateUnknown, Code: "TASK_DIAGNOSTICS_REQUIRE_REVIEW", Receipt: safeReceipt(receipt)}, now)
	}
	claim.Work.Receipt = safeReceipt(receipt)
	if err := runner.readback(ctx, claim.Work); err != nil {
		return ReconciliationResult{}, err
	}
	return store.result, nil
}

func (r Reconciler) pollKnownTask(ctx context.Context, work Work) (catalog.Receipt, error) {
	if work.Receipt.TaskID == "" {
		return work.Receipt, ErrBlocked
	}
	client, err := resolveProvider(ctx, r.Resolver, work.Scope, work.Command.Target.Account, work.Credentials)
	if err != nil {
		return work.Receipt, err
	}
	poller, ok := client.(Poller)
	if !ok {
		return work.Receipt, ErrBlocked
	}
	read, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	receipt, err := poller.PollCatalog(read, work.Command, work.Receipt)
	if receipt.TaskID != work.Receipt.TaskID || !validReceiptFor(work.Command, receipt) {
		return work.Receipt, ErrStale
	}
	return receipt, err
}

// ReconciliationReceiptPermitted requires an acknowledged final task; warnings
// still require explicit acceptance and never waive selected-field readback.
func ReconciliationReceiptPermitted(command catalog.Command, receipt catalog.Receipt, accepted bool) bool {
	if !validReceiptFor(command, receipt) || receipt.HTTPStatus < 200 || receipt.HTTPStatus >= 300 || receipt.TaskID == "" || len(receipt.Products) != 1 || receipt.Products[0].Status != "imported" {
		return false
	}
	if receipt.State == catalog.ReceiptApplied && len(receipt.Errors) == 0 && len(receipt.Products[0].Errors) == 0 {
		return true
	}
	if !accepted || receipt.State != catalog.ReceiptPartial || len(receipt.Errors) == 0 || len(receipt.Products[0].Errors) == 0 {
		return false
	}
	for _, group := range [][]catalog.FieldError{receipt.Errors, receipt.Products[0].Errors} {
		for _, issue := range group {
			if issue.Severity != "warning" || issue.Code != "warning_attribute_values_empty" {
				return false
			}
		}
	}
	return true
}

// This adapter exposes only the existing readback completion path. It cannot
// claim, dispatch, bind or enqueue an external mutation.
type readbackCompletionStore struct {
	store  ReconciliationStore
	claim  ReconciliationClaim
	result ReconciliationResult
}

func (s *readbackCompletionStore) ClaimCatalogWork(context.Context, ClaimRequest) (*Work, error) {
	return nil, ErrBlocked
}
func (s *readbackCompletionStore) GuardCatalogDispatch(context.Context, DispatchGuard) (bool, error) {
	return false, ErrBlocked
}
func (s *readbackCompletionStore) CompleteCatalogWork(ctx context.Context, c Completion) error {
	if c.State != StateReconciled {
		c.State = StateUnknown
	}
	if c.State == StateReconciled && c.Receipt.State == catalog.ReceiptPartial {
		c.Code = "READBACK_MATCHED_WITH_WARNINGS"
	}
	result, err := s.store.CompleteCatalogReconciliation(ctx, s.claim, c)
	s.result = result
	return err
}
func (s *readbackCompletionStore) finish(ctx context.Context, c Completion, now time.Time) (ReconciliationResult, error) {
	c.WorkID, c.Scope, c.ClaimToken, c.ExpectedState, c.Now = s.claim.Work.ID, s.claim.Work.Scope, s.claim.Token, StateUnknown, now
	err := s.CompleteCatalogWork(ctx, c)
	return s.result, err
}
