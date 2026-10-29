package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
)

// AutomaticReconcile asks the existing readback path to inspect an acknowledged
// write. It is never an instruction to resend the external mutation.
type AutomaticReconcile struct {
	Scope       domain.StockSyncScope
	OperationID string
}

// Optional test-lab caps only reduce the already bounded import attempt
// budgets. They never enable a provider, account, target or write.
func automaticImportLimits(provider string) (domain.ImportLimits, error) {
	limits := domain.DefaultOzonImportLimits()
	env := "STYK_TEST_AUTO_OZON_MAX_ATTEMPTS"
	if provider == "moysklad" {
		limits = domain.DefaultMoyskladImportLimits()
		env = "STYK_TEST_AUTO_MOY_MAX_ATTEMPTS"
	}
	if raw := os.Getenv(env); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > limits.Attempts {
			return domain.ImportLimits{}, errors.New("invalid automatic test read limit")
		}
		limits.Attempts = n
	}
	return limits, nil
}

// AdvanceAutomaticStock serializes short database-only scheduling steps across
// worker processes. No transaction or advisory lock spans provider I/O.
// Cycle/import identities survive restarts and import submissions are
// idempotent even if the worker dies between submission and linking the ID.
func (s *Store) AdvanceAutomaticStock(ctx context.Context, now time.Time) (*AutomaticReconcile, bool, error) {
	scope := testpublication.Scope()
	if !testpublication.Enabled(scope) {
		return nil, false, nil
	}
	now = now.UTC().Truncate(time.Microsecond)
	lockCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	conn, err := s.Pool.Acquire(lockCtx)
	if err != nil {
		return nil, false, err
	}
	var held bool
	if err = conn.QueryRow(lockCtx, `SELECT pg_try_advisory_lock(hashtextextended('styk-automatic-stock-scheduler-v1',0))`).Scan(&held); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !held {
		conn.Release()
		return nil, false, nil
	}
	defer func() {
		unlock, stop := context.WithTimeout(context.Background(), s.Timeout)
		defer stop()
		if _, e := conn.Exec(unlock, `SELECT pg_advisory_unlock(hashtextextended('styk-automatic-stock-scheduler-v1',0))`); e != nil {
			_ = conn.Hijack().Close(context.Background())
		} else {
			conn.Release()
		}
	}()

	var c domain.AutomaticStockCycle
	var revision int64
	var enabled bool
	var raw []byte
	err = s.Pool.QueryRow(lockCtx, `SELECT x.id,x.config_revision,x.state,x.started_at,COALESCE(x.moysklad_import_id,''),COALESCE(x.ozon_import_id,''),COALESCE(x.publication_id,''),x.reconcile_attempts,c.revision,c.enabled,v.snapshot FROM stock_auto_cycles x JOIN stock_auto_configs c USING(tenant_id,moysklad_account,ozon_account) JOIN stock_auto_config_audits v ON v.tenant_id=x.tenant_id AND v.moysklad_account=x.moysklad_account AND v.ozon_account=x.ozon_account AND v.revision=x.config_revision WHERE x.tenant_id=$1 AND x.moysklad_account=$2 AND x.ozon_account=$3 AND x.state IN ('STARTING','READING','WAITING_PUBLICATION') AND x.next_action_at<=$4 ORDER BY x.started_at LIMIT 1`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now).Scan(&c.ID, &c.ConfigRevision, &c.State, &c.StartedAt, &c.MoyskladImportID, &c.OzonImportID, &c.PublicationID, &c.ReconcileAttempts, &revision, &enabled, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		worked, e := s.startAutomaticCycle(lockCtx, scope, now)
		return nil, worked, e
	}
	if err != nil {
		return nil, false, err
	}
	if !enabled || revision != c.ConfigRevision {
		return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "CANCELLED", "CONFIGURATION_CHANGED", nil, now, false)
	}
	var policy domain.AutomaticStockPolicy
	if err = json.Unmarshal(raw, &policy); err != nil {
		return nil, false, err
	}
	if c.State != "WAITING_PUBLICATION" && (!policy.ValidUntil.After(now) || !policy.Enabled) {
		return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", "POLICY_EXPIRED", nil, now, false)
	}
	if c.State == "STARTING" {
		moyLimits, e := automaticImportLimits("moysklad")
		if e != nil {
			return nil, false, e
		}
		ozonLimits, e := automaticImportLimits("ozon")
		if e != nil {
			return nil, false, e
		}
		for _, provider := range []struct {
			name, account, existing string
			limits                  domain.ImportLimits
		}{
			{"moysklad", scope.MoyskladAccount, c.MoyskladImportID, moyLimits},
			{"ozon", scope.OzonAccount, c.OzonImportID, ozonLimits},
		} {
			if provider.existing != "" {
				continue
			}
			key := "automatic:" + c.ID + ":" + provider.name
			op, _, e := s.SubmitScheduledCatalogImport(lockCtx, scope.Tenant, provider.name, provider.account, key, "automatic-policy:"+strconv.FormatInt(c.ConfigRevision, 10), provider.limits)
			if errors.Is(e, ErrImportConflict) {
				_, e = s.Pool.Exec(lockCtx, `UPDATE stock_auto_cycles SET next_action_at=$2 WHERE id=$1 AND state='STARTING'`, c.ID, now.Add(5*time.Second))
				return nil, false, e
			}
			if e != nil {
				return nil, false, e
			}
			column := "moysklad_import_id"
			if provider.name == "ozon" {
				column = "ozon_import_id"
			}
			_, e = s.Pool.Exec(lockCtx, `UPDATE stock_auto_cycles SET `+column+`=$2,next_action_at=$3 WHERE id=$1 AND state='STARTING'`, c.ID, op.ID, now)
			return nil, true, e
		}
		_, err = s.Pool.Exec(lockCtx, `UPDATE stock_auto_cycles SET state='READING',next_action_at=$2 WHERE id=$1 AND state='STARTING'`, c.ID, now.Add(time.Second))
		return nil, true, err
	}
	if c.State == "READING" {
		moy, e := s.GetCatalogOperation(lockCtx, scope.Tenant, "moysklad", scope.MoyskladAccount, c.MoyskladImportID)
		if e != nil {
			return nil, false, e
		}
		ozon, e := s.GetCatalogOperation(lockCtx, scope.Tenant, "ozon", scope.OzonAccount, c.OzonImportID)
		if e != nil {
			return nil, false, e
		}
		if moy.State == "FAILED" || moy.State == "CANCELLED" || ozon.State == "FAILED" || ozon.State == "CANCELLED" {
			failed := moy
			if ozon.State == "FAILED" || ozon.State == "CANCELLED" {
				failed = ozon
			}
			outcome, retry := "RETRYABLE_READ_FAILURE", true
			if failed.Counts.HTTPStatus == 401 || failed.Counts.HTTPStatus == 403 {
				outcome, retry = "AUTHORIZATION_REQUIRED", false
			} else if failed.Counts.HTTPStatus == 429 {
				// The import has exhausted its bounded retry policy; its final
				// Retry-After is not retained. Do not guess a reset interval.
				outcome, retry = "RATE_LIMITED", false
			} else if failed.Outcome != nil && (*failed.Outcome == "CONFIGURATION_UNAVAILABLE" || *failed.Outcome == "INVALID_RESPONSE") {
				outcome, retry = "READ_CONFIGURATION_ERROR", false
			}
			metrics := automaticMetrics(c, moy, ozon, now)
			return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", outcome, metrics, now, retry)
		}
		if moy.State != "SUCCEEDED" || ozon.State != "SUCCEEDED" {
			_, e = s.Pool.Exec(lockCtx, `UPDATE stock_auto_cycles SET next_action_at=$2 WHERE id=$1 AND state='READING'`, c.ID, now.Add(2*time.Second))
			return nil, false, e
		}
		return nil, true, s.evaluateAutomaticCycle(lockCtx, scope, policy, c, moy, ozon, now)
	}
	if c.State == "WAITING_PUBLICATION" {
		op, e := s.StockPublication(lockCtx, scope, c.PublicationID)
		if e != nil {
			return nil, false, e
		}
		switch op.State {
		case "RECONCILED":
			return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "COMPLETE", "RECONCILED", map[string]any{"reconciled_operations": 1}, now, true)
		case "UNKNOWN", "PARTIAL":
			return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", "PUBLICATION_UNKNOWN", map[string]any{"unknown_outcomes": 1, "blocked_targets": 1}, now, false)
		case "FAILED", "BLOCKED", "CANCELLED":
			return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", "PUBLICATION_REJECTED", map[string]any{"blocked_targets": 1}, now, false)
		case "ACKNOWLEDGED":
			// An unavailable readback may have been an authorization failure or
			// a 429 with an unknown reset time. Do not poll it blindly. A known
			// HTTP-200 mismatch can be observed again at a bounded interval.
			for _, target := range op.Targets {
				if len(target.Readbacks) > 0 && target.Readbacks[0].Outcome == "UNAVAILABLE" {
					return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", "RECONCILIATION_PENDING", map[string]any{"blocked_targets": 1}, now, false)
				}
			}
			if c.ReconcileAttempts >= 3 {
				return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", "RECONCILIATION_PENDING", map[string]any{"blocked_targets": 1}, now, false)
			}
			tag, e := s.Pool.Exec(lockCtx, `UPDATE stock_auto_cycles SET reconcile_attempts=reconcile_attempts+1,next_action_at=$2 WHERE id=$1 AND state='WAITING_PUBLICATION' AND reconcile_attempts=$3`, c.ID, now.Add(15*time.Second), c.ReconcileAttempts)
			if e != nil || tag.RowsAffected() != 1 {
				return nil, false, e
			}
			return &AutomaticReconcile{Scope: scope, OperationID: c.PublicationID}, true, nil
		default:
			_, e = s.Pool.Exec(lockCtx, `UPDATE stock_auto_cycles SET next_action_at=$2 WHERE id=$1 AND state='WAITING_PUBLICATION'`, c.ID, now.Add(2*time.Second))
			return nil, false, e
		}
	}
	return nil, false, nil
}

func (s *Store) startAutomaticCycle(ctx context.Context, scope domain.StockSyncScope, now time.Time) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	if err = syncLock(ctx, tx, scope); err != nil {
		return false, err
	}
	var revision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM stock_auto_configs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND enabled AND next_run_at<=$4 AND NOT EXISTS(SELECT 1 FROM stock_auto_cycles x WHERE x.tenant_id=$1 AND x.moysklad_account=$2 AND x.ozon_account=$3 AND x.state IN ('STARTING','READING','WAITING_PUBLICATION')) FOR UPDATE`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	id, err := stockSyncID()
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stock_auto_cycles(id,tenant_id,moysklad_account,ozon_account,config_revision,state,started_at,next_action_at) VALUES($1,$2,$3,$4,$5,'STARTING',$6,$6)`, id, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, revision, now); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_auto_configs SET next_run_at=NULL WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func automaticMetrics(c domain.AutomaticStockCycle, moy, ozon domain.ImportOperation, now time.Time) map[string]any {
	return map[string]any{"duration_ms": now.Sub(c.StartedAt).Milliseconds(), "moysklad_requests": moy.Counts.Attempts, "ozon_requests": ozon.Counts.Attempts, "rate_limit_responses": moy.Counts.RateLimitResponses + ozon.Counts.RateLimitResponses, "evaluated_targets": 0, "changed_targets": 0, "external_writes": 0, "reconciled_operations": 0, "unknown_outcomes": 0, "blocked_targets": 0}
}

func (s *Store) evaluateAutomaticCycle(ctx context.Context, scope domain.StockSyncScope, policy domain.AutomaticStockPolicy, c domain.AutomaticStockCycle, moy, ozon domain.ImportOperation, now time.Time) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = syncLock(ctx, tx, scope); err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_auto_configs a JOIN stock_auto_cycles c USING(tenant_id,moysklad_account,ozon_account) WHERE c.id=$1 AND c.state='READING' AND a.enabled AND a.revision=c.config_revision AND a.revision=$2)`, c.ID, policy.Revision).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return nil
	}
	metrics := automaticMetrics(c, moy, ozon, now)
	block := func(outcome string, checks []string) error {
		metrics["blocked_targets"] = len(policy.Targets)
		metrics["failed_checks"] = checks
		raw, _ := json.Marshal(metrics)
		if _, e := tx.Exec(ctx, `UPDATE stock_auto_cycles SET state='BLOCKED',outcome=$2,finished_at=$3,metrics=$4 WHERE id=$1 AND state='READING'`, c.ID, outcome, now, raw); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `UPDATE stock_auto_configs SET last_cycle_at=$4,next_run_at=NULL WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND revision=$5`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now, policy.Revision); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if !policy.Enabled || !policy.ValidUntil.After(now) || len(policy.Targets) == 0 {
		return block("POLICY_EXPIRED", nil)
	}
	attestations := make([]domain.StockSyncAttestation, 0, len(policy.Targets))
	for _, t := range policy.Targets {
		attestations = append(attestations, automaticAttestation(policy, t))
	}
	review, err := s.stockSyncReview(ctx, tx, scope, attestations, now)
	if err != nil {
		return err
	}
	metrics["evaluated_targets"] = len(review.Targets)
	var failed []string
	for _, check := range review.Checks {
		if !check.Passed {
			failed = append(failed, check.Code)
		}
	}
	for _, target := range review.Targets {
		for _, check := range target.Checks {
			if !check.Passed {
				failed = append(failed, check.Code)
			}
		}
	}
	if !review.Ready || len(review.Targets) != len(policy.Targets) {
		return block("EVIDENCE_BLOCKED", failed)
	}
	// A cycle must evaluate the imports it actually requested, not another
	// older or overlapping traversal that happened to finish nearby.
	changed := 0
	for _, target := range review.Targets {
		source := target.Decision.Evidence.Source.FreshTraversal
		market := target.Decision.Evidence.Marketplace.FreshTraversal
		if source == nil || market == nil || source.OperationID != moy.ID || market.OperationID != ozon.ID {
			return block("TRAVERSAL_SUPERSEDED", []string{"CYCLE_TRAVERSAL_CHANGED"})
		}
		metrics["source_lag_ms"] = now.Sub(source.StartedAt).Milliseconds()
		metrics["source_physical"] = target.SourcePhysical
		metrics["source_reserve"] = target.SourceReserve
		metrics["marketplace_free"] = target.MarketplaceFree
		metrics["candidate"] = target.Quantity
		if target.Quantity == nil || target.MarketplaceFree == nil {
			return block("EVIDENCE_BLOCKED", []string{"STOCK_OBSERVATION_MISSING"})
		}
		// Standing test assumptions do not override an observed marketplace
		// reservation whose attribution has not been established.
		if target.MarketplaceReserved == nil || *target.MarketplaceReserved != 0 {
			return block("EVIDENCE_BLOCKED", []string{"MARKETPLACE_RESERVATION_REQUIRES_REVIEW"})
		}
		var fenced bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_publication_target_fences WHERE ozon_account=$1 AND product_id=$2 AND warehouse_id=$3)`, scope.OzonAccount, target.ProductID, target.WarehouseID).Scan(&fenced); err != nil {
			return err
		}
		if fenced {
			return block("TARGET_UNRESOLVED", []string{"PUBLICATION_UNKNOWN_OR_PENDING"})
		}
		if *target.Quantity != strconv.FormatInt(*target.MarketplaceFree, 10) {
			changed++
		}
	}
	metrics["changed_targets"] = changed
	// The live test allowlist is currently one target. Do not batch unrelated
	// targets until the production publisher has a separate approved rollout.
	if len(review.Targets) != 1 {
		return block("TARGET_POLICY_UNSUPPORTED", nil)
	}
	review.ID, err = stockSyncID()
	if err != nil {
		return err
	}
	rawReview, _ := json.Marshal(review)
	if _, err = tx.Exec(ctx, `INSERT INTO stock_sync_reviews(id,tenant_id,moysklad_account,ozon_account,ready,snapshot,created_at,expires_at) VALUES($1,$2,$3,$4,true,$5,$6,$7)`, review.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, rawReview, now, review.ExpiresAt); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_sync_settings SET review_id=$4,last_evaluation_key=$5,last_proof_check_at=$6,last_checks='[]'::jsonb WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND enabled`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, review.ID, stableSyncEvidence(review), now); err != nil {
		return err
	}
	if changed == 0 {
		raw, _ := json.Marshal(metrics)
		if _, err = tx.Exec(ctx, `UPDATE stock_auto_cycles SET state='COMPLETE',outcome='NO_CHANGE',finished_at=$2,metrics=$3 WHERE id=$1 AND state='READING'`, c.ID, now, raw); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE stock_auto_configs SET last_cycle_at=$4,next_run_at=$5 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND revision=$6 AND enabled`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now, now.Add(time.Duration(policy.IntervalSeconds)*time.Second), policy.Revision); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	op, err := createPublication(ctx, tx, review, now)
	if err != nil {
		if errors.Is(err, domain.ErrStockSyncBlocked) {
			return block("TARGET_UNRESOLVED", []string{"PUBLICATION_UNKNOWN_OR_PENDING"})
		}
		return err
	}
	metrics["external_writes"] = 0 // Updated only after a dispatch was recorded.
	raw, _ := json.Marshal(metrics)
	if _, err = tx.Exec(ctx, `UPDATE stock_auto_cycles SET state='WAITING_PUBLICATION',publication_id=$2,next_action_at=$3,metrics=$4 WHERE id=$1 AND state='READING'`, c.ID, op.ID, now.Add(time.Second), raw); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) finishAutomaticCycle(ctx context.Context, scope domain.StockSyncScope, id, state, outcome string, additions map[string]any, now time.Time, schedule bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = syncLock(ctx, tx, scope); err != nil {
		return err
	}
	var writes, readbacks int
	var uncertainDispatch bool
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE t.http_status IS NOT NULL OR t.safe_error='REQUEST_OUTCOME_UNKNOWN'), count(r.id), COALESCE(bool_or(t.safe_error='WORKER_OUTCOME_UNKNOWN'),false) FROM stock_auto_cycles c LEFT JOIN stock_publication_targets t ON t.operation_id=c.publication_id LEFT JOIN stock_publication_readbacks r ON r.target_id=t.id WHERE c.id=$1`, id).Scan(&writes, &readbacks, &uncertainDispatch); err != nil {
		return err
	}
	// The join above repeats a target for each readback. An operation contains
	// one approved test target, so write attempts are bounded to one.
	if writes > 1 {
		writes = 1
	}
	if additions == nil {
		additions = map[string]any{}
	}
	additions["external_writes"] = writes
	additions["readback_requests"] = readbacks
	additions["write_dispatch_unknown"] = uncertainDispatch
	raw, _ := json.Marshal(additions)
	var revision int64
	err = tx.QueryRow(ctx, `UPDATE stock_auto_cycles SET state=$2,outcome=$3,finished_at=$4,metrics=metrics || $5::jsonb || jsonb_build_object('duration_ms',GREATEST(0,extract(epoch from ($4-started_at))*1000)::bigint) WHERE id=$1 AND state IN ('STARTING','READING','WAITING_PUBLICATION') RETURNING config_revision`, id, state, outcome, now, raw).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var interval int
	if err = tx.QueryRow(ctx, `SELECT COALESCE((snapshot->>'interval_seconds')::int,60) FROM stock_auto_config_audits WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND revision=$4`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, revision).Scan(&interval); err != nil {
		return err
	}
	if outcome == "RETRYABLE_READ_FAILURE" && interval < 60 {
		interval = 60
	}
	var next *time.Time
	if schedule {
		t := now.Add(time.Duration(interval) * time.Second)
		next = &t
	}
	_, err = tx.Exec(ctx, `UPDATE stock_auto_configs SET last_cycle_at=$4,next_run_at=CASE WHEN enabled AND revision=$5 THEN $6 ELSE next_run_at END WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now, revision, next)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
