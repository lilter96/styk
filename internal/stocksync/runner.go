// Package stocksync dispatches frozen, conditionally proven stock quantities.
// External writes never use the read-only import queue's retry/lease semantics.
package stocksync

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/testpublication"
)

type Runner struct {
	Enabled        bool
	ControlledTest bool
	WorkerID       string
	Store          *postgres.Store
	ResolveOzon    func(context.Context, string, string) (*ozon.Client, error)
}

// MaintainPresence runs independently of a potentially long catalog traversal.
// Heartbeats make active test workers visible to the operator recovery gate;
// an expired heartbeat still cannot prove that a remote request has finished.
func (r Runner) MaintainPresence(ctx context.Context) {
	if !r.ControlledTest || !r.Enabled || r.Store == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = r.Store.TouchTestStockWorker(ctx, r.WorkerID, time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r Runner) RunNext(ctx context.Context) (bool, error) {
	if !r.Enabled {
		return false, nil
	}
	if r.Store == nil || r.ResolveOzon == nil {
		return false, errors.New("stock publisher requires a store and scoped credential resolver")
	}
	now := time.Now().UTC()
	var op *domain.StockPublication
	var err error
	if r.ControlledTest {
		if !testpublication.Enabled(testpublication.Scope()) {
			return false, nil
		}
		if err = r.Store.TouchTestStockWorker(ctx, r.WorkerID, now); err != nil {
			return false, err
		}
		if err = r.Store.RecoverControlledStockPublications(ctx, now); err != nil {
			return false, err
		}
		op, err = r.Store.NextControlledStockPublication(ctx, now)
	} else {
		if err = r.Store.RefreshStockSync(ctx, now); err != nil {
			return false, err
		}
		op, err = r.Store.NextStockPublication(ctx, now)
	}
	if err != nil || op == nil {
		return false, err
	}
	finish := func(target domain.StockPublicationTarget, state, code string, status *int) error {
		save, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.Store.Timeout)
		defer cancel()
		return r.Store.FinishStockTargetHTTP(save, op.StockSyncScope, op.ID, target.ID, state, code, status, time.Now().UTC())
	}
	finishProvider := func(target domain.StockPublicationTarget, state, code string, status *int, updated *bool, codes []string) error {
		save, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.Store.Timeout)
		defer cancel()
		return r.Store.FinishStockTargetProvider(save, op.StockSyncScope, op.ID, target.ID, state, code, status, updated, codes, time.Now().UTC())
	}
	enabled, err := r.Store.StockPublicationStillEnabled(ctx, op.StockSyncScope)
	if err != nil {
		return true, err
	}
	if !enabled {
		for _, t := range op.Targets {
			if t.State == "DISPATCHING" {
				if err = finish(t, "CANCELLED", "SYNC_DISABLED", nil); err != nil {
					return true, err
				}
			}
		}
		return true, nil
	}
	client, err := r.ResolveOzon(ctx, op.Tenant, op.OzonAccount)
	if err != nil {
		for _, t := range op.Targets {
			if t.State == "DISPATCHING" {
				if err = finish(t, "BLOCKED", "CREDENTIALS_UNAVAILABLE", nil); err != nil {
					return true, err
				}
			}
		}
		return true, nil
	}
	items := []ozon.StockUpdate{}
	targets := []domain.StockPublicationTarget{}
	for _, t := range op.Targets {
		if t.State != "DISPATCHING" {
			continue
		}
		quantity, err := strconv.ParseInt(t.Quantity, 10, 64)
		if err != nil {
			return true, errors.New("invalid frozen publication quantity")
		}
		items = append(items, ozon.StockUpdate{ProductID: t.ProductID, WarehouseID: t.WarehouseID, Quantity: quantity})
		targets = append(targets, t)
	}
	if len(items) == 0 {
		return true, nil
	}
	automatic, err := r.Store.IsAutomaticPublication(ctx, op.StockSyncScope, op.ID)
	if err != nil {
		return true, err
	}
	valid, checkErr := r.Store.StockPublicationCanDispatch(ctx, *op, time.Now().UTC())
	if checkErr != nil {
		return true, checkErr
	}
	if !valid {
		for _, t := range targets {
			if err = finish(t, "BLOCKED", "EVIDENCE_CHANGED_OR_DISABLED", nil); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	if r.ControlledTest {
		if len(items) != 1 {
			return true, errors.New("controlled test publication must contain exactly one target")
		}
		if err = client.BindControlledTestIntent(items[0], func(check context.Context) (bool, error) {
			return r.Store.StockPublicationCanDispatch(check, *op, time.Now().UTC())
		}); err != nil {
			return true, err
		}
	}
	send, cancel := context.WithTimeout(ctx, 20*time.Second)
	results, writeErr := client.UpdateStocks(send, items)
	cancel()
	if writeErr != nil {
		if errors.Is(writeErr, ozon.ErrStockWritesDisabled) {
			for _, t := range targets {
				if err = finish(t, "BLOCKED", "EVIDENCE_CHANGED_OR_DISABLED", nil); err != nil {
					return true, err
				}
			}
			return true, nil
		}
		var status *int
		var providerErr *ozon.StockWriteError
		if errors.As(writeErr, &providerErr) && providerErr.HTTPStatus != 0 {
			status = &providerErr.HTTPStatus
		}
		for _, t := range targets {
			if err = finish(t, "UNKNOWN", "REQUEST_OUTCOME_UNKNOWN", status); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	for i, t := range targets {
		if i >= len(results) {
			if err = finish(t, "UNKNOWN", "TARGET_RESULT_MISSING", nil); err != nil {
				return true, err
			}
			continue
		}
		result := results[i]
		if result.ProductID != t.ProductID || result.WarehouseID != t.WarehouseID {
			if err = finish(t, "UNKNOWN", "TARGET_RESULT_MISMATCH", nil); err != nil {
				return true, err
			}
			continue
		}
		state := result.State
		if (r.ControlledTest || automatic) && state == "SUCCEEDED" {
			state = "ACKNOWLEDGED"
		}
		status := result.HTTPStatus
		if err = finishProvider(t, state, result.SafeError, &status, result.Updated, result.ErrorCodes); err != nil {
			return true, err
		}
		if (r.ControlledTest || automatic) && state == "ACKNOWLEDGED" && t.Decision.Evidence.ProductMapping != nil {
			readCtx, readCancel := context.WithTimeout(ctx, 20*time.Second)
			quantity, readErr := client.ReadStockQuantity(readCtx, t.ProductID, t.WarehouseID, t.Decision.Evidence.ProductMapping.OfferID)
			readCancel()
			var observed *int64
			if readErr == nil {
				observed = &quantity
			}
			save, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), r.Store.Timeout)
			err = r.Store.RecordStockReadback(save, op.StockSyncScope, op.ID, t.ID, observed, time.Now().UTC())
			saveCancel()
			if err != nil {
				return true, err
			}
		}
	}
	return true, nil
}

// Reconcile performs read-only checks. CONFIRMED_EFFECTIVE means the target
// returned the requested free quantity; it proves neither freshness nor causality.
// A mismatching/unavailable observation leaves UNKNOWN fenced against rewrites.
func (r Runner) Reconcile(ctx context.Context, scope domain.StockSyncScope, id string) (domain.StockSyncView, error) {
	if r.Store == nil || r.ResolveOzon == nil {
		return domain.StockSyncView{}, errors.New("stock reconciler unavailable")
	}
	now := time.Now().UTC()
	if err := r.Store.MarkStockPublicationUnknown(ctx, scope, id, now); err != nil {
		return domain.StockSyncView{}, err
	}
	op, err := r.Store.StockPublication(ctx, scope, id)
	if err != nil {
		return domain.StockSyncView{}, err
	}
	client, err := r.ResolveOzon(ctx, scope.Tenant, scope.OzonAccount)
	if err != nil {
		return domain.StockSyncView{}, errors.New("stock reconciliation credentials unavailable")
	}
	for _, t := range op.Targets {
		if (t.State != "UNKNOWN" && t.State != "ACKNOWLEDGED") || t.Decision.Evidence.ProductMapping == nil {
			continue
		}
		offer := t.Decision.Evidence.ProductMapping.OfferID
		quantity, readErr := client.ReadStockQuantity(ctx, t.ProductID, t.WarehouseID, offer)
		if t.State == "ACKNOWLEDGED" {
			var observed *int64
			if readErr == nil {
				observed = &quantity
			}
			if err = r.Store.RecordStockReadback(ctx, scope, id, t.ID, observed, time.Now().UTC()); err != nil {
				return domain.StockSyncView{}, err
			}
			continue
		}
		if scope == testpublication.Scope() {
			var observed *int64
			if readErr == nil {
				observed = &quantity
			}
			if err = r.Store.RecordUnknownStockReadback(ctx, scope, id, t.ID, observed, time.Now().UTC()); err != nil {
				return domain.StockSyncView{}, err
			}
			// Controlled supersession preserves the original UNKNOWN outcome even
			// if a read happens to match the requested quantity.
			continue
		}
		if readErr != nil {
			if ctx.Err() != nil {
				break
			}
			continue
		}
		desired, e := strconv.ParseInt(t.Quantity, 10, 64)
		if e == nil && quantity == desired {
			if err = r.Store.FinishStockTarget(ctx, scope, id, t.ID, "CONFIRMED_EFFECTIVE", "READBACK_MATCHED_EFFECT", time.Now().UTC()); err != nil {
				return domain.StockSyncView{}, err
			}
		}
	}
	return r.Store.StockSyncStatus(ctx, scope, time.Now().UTC())
}
