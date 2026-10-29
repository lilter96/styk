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
)

type Runner struct {
	Store       *postgres.Store
	ResolveOzon func(context.Context, string, string) (*ozon.Client, error)
}

func (r Runner) RunNext(ctx context.Context) (bool, error) {
	if r.Store == nil || r.ResolveOzon == nil {
		return false, errors.New("stock publisher requires a store and scoped credential resolver")
	}
	now := time.Now().UTC()
	if err := r.Store.RefreshStockSync(ctx, now); err != nil {
		return false, err
	}
	op, err := r.Store.NextStockPublication(ctx, now)
	if err != nil || op == nil {
		return false, err
	}
	finish := func(target domain.StockPublicationTarget, state, code string) error {
		save, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.Store.Timeout)
		defer cancel()
		return r.Store.FinishStockTarget(save, op.StockSyncScope, op.ID, target.ID, state, code, time.Now().UTC())
	}
	enabled, err := r.Store.StockPublicationStillEnabled(ctx, op.StockSyncScope)
	if err != nil {
		return true, err
	}
	if !enabled {
		for _, t := range op.Targets {
			if t.State == "DISPATCHING" {
				if err = finish(t, "CANCELLED", "SYNC_DISABLED"); err != nil {
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
				if err = finish(t, "BLOCKED", "CREDENTIALS_UNAVAILABLE"); err != nil {
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
	valid, checkErr := r.Store.StockPublicationCanDispatch(ctx, *op, time.Now().UTC())
	if checkErr != nil {
		return true, checkErr
	}
	if !valid {
		for _, t := range targets {
			if err = finish(t, "BLOCKED", "EVIDENCE_CHANGED_OR_DISABLED"); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	send, cancel := context.WithTimeout(ctx, 20*time.Second)
	results, writeErr := client.UpdateStocks(send, items)
	cancel()
	if writeErr != nil {
		for _, t := range targets {
			if err = finish(t, "UNKNOWN", "REQUEST_OUTCOME_UNKNOWN"); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	for i, t := range targets {
		if i >= len(results) {
			if err = finish(t, "UNKNOWN", "TARGET_RESULT_MISSING"); err != nil {
				return true, err
			}
			continue
		}
		result := results[i]
		if result.ProductID != t.ProductID || result.WarehouseID != t.WarehouseID {
			if err = finish(t, "UNKNOWN", "TARGET_RESULT_MISMATCH"); err != nil {
				return true, err
			}
			continue
		}
		if err = finish(t, result.State, result.SafeError); err != nil {
			return true, err
		}
	}
	return true, nil
}

// Reconcile performs read-only checks. CONFIRMED_EFFECTIVE means the target
// currently has the requested free quantity; it does not prove causal execution.
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
		if t.State != "UNKNOWN" || t.Decision.Evidence.ProductMapping == nil {
			continue
		}
		offer := t.Decision.Evidence.ProductMapping.OfferID
		quantity, readErr := client.ReadStockQuantity(ctx, t.ProductID, t.WarehouseID, offer)
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
