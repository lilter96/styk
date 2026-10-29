package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/config"
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

func automaticReadFailure(operations ...domain.ImportOperation) (string, bool) {
	for _, op := range operations {
		if (op.State == "FAILED" || op.State == "CANCELLED") && (op.Counts.HTTPStatus == 401 || op.Counts.HTTPStatus == 403) {
			return "AUTHORIZATION_REQUIRED", false
		}
	}
	for _, op := range operations {
		if (op.State == "FAILED" || op.State == "CANCELLED") && (op.Counts.HTTPStatus == 429 || op.Counts.RateLimitResponses > 0) {
			return "RATE_LIMITED", false
		}
	}
	for _, op := range operations {
		if (op.State == "FAILED" || op.State == "CANCELLED") && op.Outcome != nil && (*op.Outcome == "CONFIGURATION_UNAVAILABLE" || *op.Outcome == "INVALID_RESPONSE") {
			return "READ_CONFIGURATION_ERROR", false
		}
	}
	return "RETRYABLE_READ_FAILURE", true
}

// AdvanceAutomaticStock serializes short database-only scheduling steps across
// worker processes. No transaction or advisory lock spans provider I/O.
// Cycle/import identities survive restarts and import submissions are
// idempotent even if the worker dies between submission and linking the ID.
func (s *Store) AdvanceAutomaticStock(ctx context.Context, now time.Time) (*AutomaticReconcile, bool, error) {
	scope := testpublication.Scope()
	if !testpublication.Enabled(scope) {
		// Only the pre-existing loopback simulator may exercise other scopes.
		// The live database's account constraint still rejects such policies.
		_, simulator := config.StockPublicationTestEndpoint()
		if !simulator {
			return nil, false, nil
		}
		err := s.Pool.QueryRow(ctx, `SELECT tenant_id,moysklad_account,ozon_account FROM stock_auto_configs c WHERE enabled AND tenant_id LIKE 'synthetic-auto-%' AND (next_run_at<=$1 OR EXISTS(SELECT 1 FROM stock_auto_cycles x WHERE x.tenant_id=c.tenant_id AND x.moysklad_account=c.moysklad_account AND x.ozon_account=c.ozon_account AND x.state IN ('STARTING','READING','WAITING_PUBLICATION') AND x.next_action_at<=$1)) ORDER BY LEAST(COALESCE(next_run_at,'infinity'::timestamptz),COALESCE((SELECT min(x.next_action_at) FROM stock_auto_cycles x WHERE x.tenant_id=c.tenant_id AND x.moysklad_account=c.moysklad_account AND x.ozon_account=c.ozon_account AND x.state IN ('STARTING','READING','WAITING_PUBLICATION')),'infinity'::timestamptz)),tenant_id LIMIT 1`, now).Scan(&scope.Tenant, &scope.MoyskladAccount, &scope.OzonAccount)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
	return s.advanceAutomaticStockScope(ctx, scope, now)
}

func (s *Store) advanceAutomaticStockScope(ctx context.Context, scope domain.StockSyncScope, now time.Time) (*AutomaticReconcile, bool, error) {
	if !automaticScopeAllowed(scope) {
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
		terminal := func(state string) bool { return state == "SUCCEEDED" || state == "FAILED" || state == "CANCELLED" }
		if !terminal(moy.State) || !terminal(ozon.State) {
			_, e = s.Pool.Exec(lockCtx, `UPDATE stock_auto_cycles SET next_action_at=$2 WHERE id=$1 AND state='READING'`, c.ID, now.Add(2*time.Second))
			return nil, false, e
		}
		if moy.State == "FAILED" || moy.State == "CANCELLED" || ozon.State == "FAILED" || ozon.State == "CANCELLED" {
			// If either provider denies access or rate-limits, never let the
			// other provider's transient failure hide the stricter condition.
			outcome, retry := automaticReadFailure(moy, ozon)
			metrics := automaticMetrics(c, moy, ozon, now)
			return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", outcome, metrics, now, retry)
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
			if len(policy.Targets) > 1 {
				return nil, true, s.finishAutomaticTarget(lockCtx, scope, c, "RECONCILED", now)
			}
			return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "COMPLETE", "RECONCILED", map[string]any{"reconciled_operations": 1}, now, true)
		case "UNKNOWN", "PARTIAL":
			if len(policy.Targets) > 1 {
				return nil, true, s.finishAutomaticTarget(lockCtx, scope, c, "PUBLICATION_UNKNOWN", now)
			}
			return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", "PUBLICATION_UNKNOWN", map[string]any{"unknown_outcomes": 1, "blocked_targets": 1}, now, len(policy.Targets) > 1)
		case "FAILED", "BLOCKED", "CANCELLED":
			if len(policy.Targets) > 1 {
				return nil, true, s.finishAutomaticTarget(lockCtx, scope, c, "PUBLICATION_REJECTED", now)
			}
			return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", "PUBLICATION_REJECTED", map[string]any{"blocked_targets": 1}, now, len(policy.Targets) > 1)
		case "ACKNOWLEDGED":
			// An unavailable readback may have been an authorization failure or
			// a 429 with an unknown reset time. Do not poll it blindly. A known
			// HTTP-200 mismatch can be observed again at a bounded interval.
			for _, target := range op.Targets {
				if len(target.Readbacks) > 0 && target.Readbacks[0].Outcome == "UNAVAILABLE" {
					if len(policy.Targets) > 1 {
						return nil, true, s.finishAutomaticTarget(lockCtx, scope, c, "RECONCILIATION_PENDING", now)
					}
					return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", "RECONCILIATION_PENDING", map[string]any{"blocked_targets": 1}, now, len(policy.Targets) > 1)
				}
			}
			if c.ReconcileAttempts >= 3 {
				if len(policy.Targets) > 1 {
					return nil, true, s.finishAutomaticTarget(lockCtx, scope, c, "RECONCILIATION_PENDING", now)
				}
				return nil, true, s.finishAutomaticCycle(lockCtx, scope, c.ID, "BLOCKED", "RECONCILIATION_PENDING", map[string]any{"blocked_targets": 1}, now, len(policy.Targets) > 1)
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
		if metrics["blocked_targets"] == 0 {
			metrics["blocked_targets"] = len(policy.Targets)
		}
		metrics["failed_checks"] = checks
		raw, _ := json.Marshal(metrics)
		if _, e := tx.Exec(ctx, `UPDATE stock_auto_cycles SET metrics=metrics || $2::jsonb WHERE id=$1 AND state='READING'`, c.ID, raw); e != nil {
			return e
		}
		if e := s.finishAutomaticCycleTx(ctx, tx, scope, c.ID, "BLOCKED", outcome, nil, now, outcome == "TARGETS_NEED_ATTENTION" && len(policy.Targets) > 1); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if !policy.Enabled || !policy.ValidUntil.After(now) || len(policy.Targets) == 0 {
		return block("POLICY_EXPIRED", nil)
	}
	// A completed publication is skipped for the rest of this traversal. This
	// lets independent targets reuse the same two imports without reposting a
	// target whose pre-write marketplace observation is still in that snapshot.
	processed := map[string]bool{}
	var oldMetrics []byte
	if err = tx.QueryRow(ctx, `SELECT metrics FROM stock_auto_cycles WHERE id=$1`, c.ID).Scan(&oldMetrics); err != nil {
		return err
	}
	old := map[string]json.RawMessage{}
	_ = json.Unmarshal(oldMetrics, &old)
	targetResults := map[string]map[string]any{}
	_ = json.Unmarshal(old["target_results"], &targetResults)
	rows, err := tx.Query(ctx, `SELECT cp.product_id,cp.warehouse_id,cp.publication_id,cp.outcome,t.readback_quantity FROM stock_auto_cycle_publications cp JOIN stock_publication_targets t ON t.operation_id=cp.publication_id WHERE cp.cycle_id=$1`, c.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var product, warehouse, operation, outcome string
		var readback *string
		if err = rows.Scan(&product, &warehouse, &operation, &outcome, &readback); err != nil {
			rows.Close()
			return err
		}
		key := product + ":" + warehouse
		processed[key] = true
		if targetResults[key] == nil {
			targetResults[key] = map[string]any{}
		}
		targetResults[key]["outcome"], targetResults[key]["publication_id"], targetResults[key]["readback_quantity"] = outcome, operation, readback
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Review targets independently; a fenced or invalid product must not block
	// another physical target. Operations retain one-target write fencing.
	type candidate struct {
		review domain.StockSyncReview
		target domain.StockSyncTarget
		last   *time.Time
	}
	var chosen, noOp *candidate
	changed, blocked := 0, 0
	var failed []string
	for _, configured := range policy.Targets {
		key := configured.ProductID + ":" + configured.WarehouseID
		if processed[key] {
			metrics["evaluated_targets"] = metrics["evaluated_targets"].(int) + 1
			continue
		}
		r, e := s.stockSyncReview(ctx, tx, scope, []domain.StockSyncAttestation{automaticAttestation(policy, configured)}, now)
		if e != nil {
			return e
		}
		metrics["evaluated_targets"] = metrics["evaluated_targets"].(int) + 1
		if !r.Ready || len(r.Targets) != 1 {
			blocked++
			firstFailure := len(failed)
			for _, check := range r.Checks {
				if !check.Passed {
					failed = append(failed, check.Code)
				}
			}
			for _, target := range r.Targets {
				for _, check := range target.Checks {
					if !check.Passed {
						failed = append(failed, check.Code)
					}
				}
			}
			targetResults[key] = map[string]any{"outcome": "BLOCKED", "checks": append([]string(nil), failed[firstFailure:]...)}
			continue
		}
		target := r.Targets[0]
		targetResults[key] = map[string]any{"outcome": "PENDING", "source_physical": target.SourcePhysical, "source_reserve": target.SourceReserve, "marketplace_free": target.MarketplaceFree, "candidate": target.Quantity}
		source, market := target.Decision.Evidence.Source.FreshTraversal, target.Decision.Evidence.Marketplace.FreshTraversal
		if source == nil || market == nil || source.OperationID != moy.ID || market.OperationID != ozon.ID {
			blocked++
			failed = append(failed, "CYCLE_TRAVERSAL_CHANGED")
			targetResults[key]["outcome"] = "BLOCKED"
			targetResults[key]["checks"] = []string{"CYCLE_TRAVERSAL_CHANGED"}
			continue
		}
		if target.Quantity == nil || target.MarketplaceFree == nil {
			blocked++
			failed = append(failed, "STOCK_OBSERVATION_MISSING")
			targetResults[key]["outcome"] = "BLOCKED"
			targetResults[key]["checks"] = []string{"STOCK_OBSERVATION_MISSING"}
			continue
		}
		if target.MarketplaceReserved == nil || *target.MarketplaceReserved != 0 {
			blocked++
			failed = append(failed, "MARKETPLACE_RESERVATION_REQUIRES_REVIEW")
			targetResults[key]["outcome"] = "BLOCKED"
			targetResults[key]["checks"] = []string{"MARKETPLACE_RESERVATION_REQUIRES_REVIEW"}
			continue
		}
		var fenced, rejected bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_publication_target_fences WHERE ozon_account=$1 AND product_id=$2 AND warehouse_id=$3), EXISTS(SELECT 1 FROM stock_auto_cycles c JOIN stock_auto_cycle_publications cp ON cp.cycle_id=c.id WHERE c.tenant_id=$4 AND c.moysklad_account=$5 AND c.ozon_account=$1 AND c.config_revision=$6 AND cp.outcome='PUBLICATION_REJECTED' AND cp.product_id=$2 AND cp.warehouse_id=$3)`, scope.OzonAccount, target.ProductID, target.WarehouseID, scope.Tenant, scope.MoyskladAccount, policy.Revision).Scan(&fenced, &rejected); err != nil {
			return err
		}
		if fenced || rejected {
			blocked++
			failed = append(failed, "PUBLICATION_UNKNOWN_OR_PENDING")
			targetResults[key]["outcome"] = "BLOCKED"
			targetResults[key]["checks"] = []string{"PUBLICATION_UNKNOWN_OR_PENDING"}
			continue
		}
		item := candidate{review: r, target: target}
		if *target.Quantity == strconv.FormatInt(*target.MarketplaceFree, 10) {
			targetResults[key]["outcome"] = "NO_CHANGE"
			if noOp == nil {
				v := item
				noOp = &v
			}
			continue
		}
		changed++
		if err = tx.QueryRow(ctx, `SELECT max(p.created_at) FROM stock_publications p JOIN stock_publication_targets t ON t.operation_id=p.id WHERE p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND t.product_id=$4 AND t.warehouse_id=$5`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, target.ProductID, target.WarehouseID).Scan(&item.last); err != nil {
			return err
		}
		if chosen == nil || (item.last == nil && chosen.last != nil) || (item.last != nil && chosen.last != nil && item.last.Before(*chosen.last)) {
			v := item
			chosen = &v
		}
	}
	metrics["changed_targets"], metrics["blocked_targets"], metrics["failed_checks"], metrics["target_results"] = changed+len(processed), blocked, failed, targetResults
	if chosen == nil && len(processed) > 0 {
		var failedPublications int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM stock_auto_cycle_publications WHERE cycle_id=$1 AND outcome IN ('PUBLICATION_UNKNOWN','PUBLICATION_REJECTED','RECONCILIATION_PENDING')`, c.ID).Scan(&failedPublications); err != nil {
			return err
		}
		raw, _ := json.Marshal(metrics)
		if _, err = tx.Exec(ctx, `UPDATE stock_auto_cycles SET metrics=metrics || $2::jsonb WHERE id=$1 AND state='READING'`, c.ID, raw); err != nil {
			return err
		}
		state, outcome := "COMPLETE", "RECONCILED"
		if blocked > 0 || failedPublications > 0 {
			state, outcome = "BLOCKED", "TARGETS_NEED_ATTENTION"
		}
		if err = s.finishAutomaticCycleTx(ctx, tx, scope, c.ID, state, outcome, nil, now, true); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if chosen == nil && blocked > 0 {
		outcome := "TARGETS_NEED_ATTENTION"
		if len(policy.Targets) == 1 {
			outcome = "EVIDENCE_BLOCKED"
		}
		return block(outcome, failed)
	}
	if chosen == nil {
		chosen = noOp
	}
	if chosen == nil {
		return block("EVIDENCE_BLOCKED", failed)
	}
	review, target := chosen.review, chosen.target
	metrics["source_traversal_age_ms"] = now.Sub(target.Decision.Evidence.Source.FreshTraversal.StartedAt).Milliseconds()
	// Neither import is an atomic source snapshot or a provider change event;
	// the true time from an external stock change to reconciliation is unknown.
	metrics["sync_lag_ms"] = nil
	metrics["source_physical"], metrics["source_reserve"], metrics["marketplace_free"], metrics["candidate"] = target.SourcePhysical, target.SourceReserve, target.MarketplaceFree, target.Quantity
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
	if _, err = tx.Exec(ctx, `INSERT INTO stock_auto_cycle_publications(cycle_id,publication_id,product_id,warehouse_id) VALUES($1,$2,$3,$4)`, c.ID, op.ID, target.ProductID, target.WarehouseID); err != nil {
		return err
	}
	metrics["external_writes"] = 0 // Updated only after a dispatch was recorded.
	raw, _ := json.Marshal(metrics)
	if _, err = tx.Exec(ctx, `UPDATE stock_auto_cycles SET state='WAITING_PUBLICATION',publication_id=$2,next_action_at=$3,metrics=$4 WHERE id=$1 AND state='READING'`, c.ID, op.ID, now.Add(time.Second), raw); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// A completed or fenced target never causes another import within this cycle.
// Its outcome is recorded before evaluating the remaining independent targets.
func (s *Store) finishAutomaticTarget(ctx context.Context, scope domain.StockSyncScope, c domain.AutomaticStockCycle, outcome string, now time.Time) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = syncLock(ctx, tx, scope); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE stock_auto_cycle_publications SET outcome=$3 WHERE cycle_id=$1 AND publication_id=$2 AND (outcome='' OR outcome=$3)`, c.ID, c.PublicationID, outcome)
	if err != nil || tag.RowsAffected() != 1 {
		if err == nil {
			err = errors.New("automatic target outcome missing or changed")
		}
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_auto_cycles SET state='READING',next_action_at=$2,reconcile_attempts=0 WHERE id=$1 AND state='WAITING_PUBLICATION' AND publication_id=$3`, c.ID, now, c.PublicationID); err != nil {
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
	if err = s.finishAutomaticCycleTx(ctx, tx, scope, id, state, outcome, additions, now, schedule); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) finishAutomaticCycleTx(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, id, state, outcome string, additions map[string]any, now time.Time, schedule bool) error {
	if outcome == "RECONCILED" || outcome == "PUBLICATION_UNKNOWN" || outcome == "PUBLICATION_REJECTED" || outcome == "RECONCILIATION_PENDING" {
		if _, err := tx.Exec(ctx, `UPDATE stock_auto_cycle_publications cp SET outcome=$2 FROM stock_auto_cycles c WHERE c.id=$1 AND cp.cycle_id=c.id AND cp.publication_id=c.publication_id AND cp.outcome=''`, id, outcome); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_auto_cycles c SET metrics=jsonb_set(c.metrics,ARRAY['target_results',cp.product_id||':'||cp.warehouse_id],COALESCE(c.metrics #> ARRAY['target_results',cp.product_id||':'||cp.warehouse_id],'{}'::jsonb) || jsonb_build_object('outcome',$2::text,'publication_id',cp.publication_id,'readback_quantity',t.readback_quantity),true) FROM stock_auto_cycle_publications cp JOIN stock_publication_targets t ON t.operation_id=cp.publication_id WHERE c.id=$1 AND cp.cycle_id=c.id AND cp.publication_id=c.publication_id AND c.metrics ? 'target_results'`, id, outcome); err != nil {
			return err
		}
	}
	var writes, readbacks, writeRateLimits, previousRateLimits int
	var previouslyBlocked, reconciled, unknown, childBlocked int
	var uncertainDispatch bool
	err := tx.QueryRow(ctx, `SELECT count(DISTINCT t.id) FILTER(WHERE t.http_status IS NOT NULL OR t.safe_error='REQUEST_OUTCOME_UNKNOWN'), count(r.id), COALESCE(bool_or(t.safe_error='WORKER_OUTCOME_UNKNOWN'),false),COALESCE(max((c.metrics->>'blocked_targets')::int),0),count(DISTINCT t.id) FILTER(WHERE t.http_status=429),COALESCE(max((c.metrics->>'rate_limit_responses')::int),0),count(DISTINCT cp.publication_id) FILTER(WHERE cp.outcome='RECONCILED'),count(DISTINCT cp.publication_id) FILTER(WHERE cp.outcome='PUBLICATION_UNKNOWN'),count(DISTINCT cp.publication_id) FILTER(WHERE cp.outcome IN ('PUBLICATION_UNKNOWN','PUBLICATION_REJECTED','RECONCILIATION_PENDING')) FROM stock_auto_cycles c LEFT JOIN stock_auto_cycle_publications cp ON cp.cycle_id=c.id LEFT JOIN stock_publication_targets t ON t.operation_id=cp.publication_id LEFT JOIN stock_publication_readbacks r ON r.target_id=t.id WHERE c.id=$1`, id).Scan(&writes, &readbacks, &uncertainDispatch, &previouslyBlocked, &writeRateLimits, &previousRateLimits, &reconciled, &unknown, &childBlocked)
	if err != nil {
		return err
	}
	if additions == nil {
		additions = map[string]any{}
	}
	additions["external_writes"] = writes
	additions["readback_requests"] = readbacks
	additions["write_dispatch_unknown"] = uncertainDispatch
	additions["rate_limit_responses"] = previousRateLimits + writeRateLimits
	additions["reconciled_operations"] = reconciled
	additions["unknown_outcomes"] = unknown
	additions["blocked_targets"] = previouslyBlocked + childBlocked
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
	return nil
}
