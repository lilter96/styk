// Package ozonjob runs only bounded, read-only catalog imports on outbox claims.
package ozonjob

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
)

type Runner struct {
	MoyskladAccount string
	MoyskladClient  *moysklad.Client
	Store           *postgres.Store
	Account         string
	Client          *ozon.Client
}

func counts(c ozon.Counts) domain.ImportCounts {
	return domain.ImportCounts{Requests: c.Requests, Attempts: c.Attempts, Pages: c.Pages, CompletedRequests: c.CompletedRequests, CompletedPages: c.CompletedPages, Products: c.Products, Warehouses: c.Warehouses, Observations: c.Observations}
}

var errProgress = errors.New("cannot persist import progress")

func (r Runner) ProcessImport(parent context.Context, t domain.Task, lease time.Duration) error {
	if r.Store == nil || lease <= 0 {
		return errors.New("invalid import worker")
	}
	// Renewal is bounded by the attempt and joined before returning. Losing the
	// lease cancels traversal; persistence still checks the claim under row lock.
	attempt, cancelAttempt := context.WithCancel(parent)
	heartbeat, stopHeartbeat := context.WithCancel(attempt)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		interval := lease / 3
		if interval <= 0 {
			interval = time.Millisecond
		}
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-heartbeat.Done():
				return
			case <-tick.C:
				if err := r.Store.RenewImportClaim(heartbeat, t, lease); err != nil {
					if heartbeat.Err() == nil {
						cancelAttempt()
					}
					return
				}
			}
		}
	}()
	defer func() { stopHeartbeat(); cancelAttempt(); wg.Wait() }()
	op, err := r.Store.ImportForClaim(attempt, t)
	if err != nil {
		return err
	}
	if !op.Limits.ValidFor(op.Provider) {
		return r.Store.FailImport(attempt, t, "CONFIGURATION_UNAVAILABLE", "", domain.ImportCounts{})
	}
	isMoy := op.Provider == "moysklad" && op.Type == domain.MoyskladCatalogImport && t.Type == domain.MoyskladCatalogTask
	isOzon := op.Provider == "ozon" && op.Type == domain.OzonCatalogImport && t.Type == domain.OzonCatalogTask
	if (!isMoy && !isOzon) || (isMoy && (r.MoyskladClient == nil || r.MoyskladAccount != op.Account)) || (isOzon && (r.Client == nil || r.Account != op.Account)) {
		return r.Store.FailImport(attempt, t, "CONFIGURATION_UNAVAILABLE", "", domain.ImportCounts{})
	}
	if op.AttemptStartedAt == nil {
		return errors.New("missing attempt start")
	}
	deadline := op.AttemptStartedAt.Add(time.Duration(op.Limits.DurationNS))
	ctx, cancel := context.WithDeadline(attempt, deadline)
	defer cancel()
	l := ozon.Limits{Requests: op.Limits.Requests, Attempts: op.Limits.Attempts, Pages: op.Limits.Pages, Entities: op.Limits.Products + op.Limits.Warehouses, Products: op.Limits.Products, Warehouses: op.Limits.Warehouses, Observations: op.Limits.Observations, Postings: 1, ResponseBytes: op.Limits.ResponseBytes, Duration: time.Duration(op.Limits.DurationNS)}
	progress := func(ctx context.Context, phase string, c ozon.Counts) error {
		if e := r.Store.ImportProgress(ctx, t, phase, counts(c)); e != nil {
			return errors.Join(errProgress, e)
		}
		return nil
	}
	started := time.Now()
	var observed domain.ImportCounts
	var snap domain.OzonImport
	var moySnap domain.InventoryImport
	var readErr error
	if isMoy {
		moySnap, observed, readErr = r.MoyskladClient.ReadBoundedProgress(ctx, op.Account, op.Limits, func(ctx context.Context, phase string, c domain.ImportCounts) error {
			if e := r.Store.ImportProgress(ctx, t, phase, c); e != nil {
				return errors.Join(errProgress, e)
			}
			return nil
		})
	} else {
		var c ozon.Counts
		snap, c, readErr = r.Client.ReadBoundedProgress(ctx, l, progress)
		observed = counts(c)
	}
	if readErr != nil {
		if parent.Err() != nil {
			return parent.Err()
		}
		if errors.Is(readErr, postgres.ErrImportClaimLost) {
			return postgres.ErrImportClaimLost
		}
		execution := ozon.Result(started, ozon.Counts{}, readErr)
		outcome := string(execution.Outcome)
		resource := execution.LimitingResource
		var ml *moysklad.LimitError
		var ma *moysklad.APIError
		if errors.As(readErr, &ml) {
			outcome, resource = "LIMIT_REACHED", ml.Resource
		}
		if errors.As(readErr, &ma) || errors.Is(readErr, moysklad.ErrTransport) {
			outcome = "API_FAILURE"
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || (errors.Is(readErr, context.DeadlineExceeded) && !errors.Is(readErr, errProgress)) {
			outcome, resource = "LIMIT_REACHED", "duration"
		} else if errors.Is(readErr, errProgress) {
			outcome = "PERSISTENCE_FAILURE"
		}
		return r.Store.FailImport(attempt, t, outcome, resource, observed)
	}
	var fingerprint string
	if isMoy {
		fingerprint, err = application.InventoryFingerprint(&moySnap)
	} else {
		fingerprint, err = application.OzonFingerprint(&snap)
	}
	if err != nil {
		return r.Store.FailImport(attempt, t, "INVALID_RESPONSE", "", observed)
	}
	if err = ctx.Err(); err != nil {
		if parent.Err() != nil {
			return parent.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return r.Store.FailImport(attempt, t, "LIMIT_REACHED", "duration", observed)
		}
		return r.Store.FailImport(attempt, t, "CANCELLED", "", observed)
	}
	if err = r.Store.ImportProgress(ctx, t, "PERSISTING", observed); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
			return r.Store.FailImport(attempt, t, "LIMIT_REACHED", "duration", observed)
		}
		return err
	}
	// The commit transaction locks the task row. Stop renewal before taking
	// that lock so a renewal timeout cannot cancel our own transaction; renew
	// once more first. The transaction fences again and rolls back if the lease
	// expires before its terminal update.
	stopHeartbeat()
	wg.Wait()
	if err = r.Store.RenewImportClaim(ctx, t, lease); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
			return r.Store.FailImport(attempt, t, "LIMIT_REACHED", "duration", observed)
		}
		return err
	}
	if isMoy {
		err = r.Store.CommitClaimedMoyskladImport(ctx, t, moySnap, fingerprint, observed, time.Duration(op.Limits.DatabaseTimeoutNS))
	} else {
		err = r.Store.CommitClaimedOzonImport(ctx, t, snap, fingerprint, observed, time.Duration(op.Limits.DatabaseTimeoutNS))
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, postgres.ErrImportClaimLost) {
		return err
	}
	if errors.Is(err, postgres.ErrImportCommitUnknown) {
		// The task row lock serializes this read with any ambiguous commit. Never
		// mark failure or run a new traversal until persisted state is inspected.
		resolved, resolveErr := r.Store.ResolveImportCommit(attempt, t)
		if resolveErr != nil {
			return fmt.Errorf("commit reconciliation unavailable")
		}
		if resolved {
			return nil
		}
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return r.Store.FailImport(attempt, t, "LIMIT_REACHED", "duration", observed)
	}
	return r.Store.FailImport(attempt, t, "PERSISTENCE_FAILURE", "", observed)
}
