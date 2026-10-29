package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/testpublication"
)

var automaticProofCodes = []string{commitment.ProofUnit, commitment.ProofBaseline, commitment.ProofSourceOrdering, commitment.ProofUnassigned, commitment.ProofAttribution, commitment.ProofPostingCoverage, commitment.ProofCompeting, commitment.ProofBound, commitment.ProofCustomerOrders}

// Synthetic scopes are usable only with the existing loopback-only simulator.
// The migrated live database still independently restricts configurations to
// the owner's fixed account pair; this branch permits isolated-schema tests.
func automaticScopeAllowed(scope domain.StockSyncScope) bool {
	if testpublication.Enabled(scope) {
		return true
	}
	_, simulator := config.StockPublicationTestEndpoint()
	return simulator && scope.Valid() && strings.HasPrefix(scope.Tenant, "synthetic-auto-")
}

// automaticAttestation preserves the time of the operator's original policy
// acceptance. It never invents a new verification time during later cycles.
func automaticAttestation(p domain.AutomaticStockPolicy, target domain.AutomaticStockTarget) domain.StockSyncAttestation {
	a := domain.StockSyncAttestation{ProductID: target.ProductID, WarehouseID: target.WarehouseID, Unreflected: target.Unreflected, UnseenBound: target.UnseenBound, SafetyStock: target.SafetyStock, AutomaticPolicyRevision: p.Revision}
	reference := fmt.Sprintf("standing OPERATOR_ATTESTED test policy revision %d; account and target scoped; no independent proof of order coverage or competing exposure", p.Revision)
	for _, code := range automaticProofCodes {
		a.Proofs = append(a.Proofs, domain.StockSyncProof{Code: code, Reference: reference, AsOf: p.CreatedAt, ValidUntil: p.ValidUntil})
	}
	return a
}

func authorizedAutomaticAttestation(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, a domain.StockSyncAttestation, now time.Time) (*domain.AutomaticStockTarget, error) {
	if !automaticScopeAllowed(scope) || a.AutomaticPolicyRevision <= 0 {
		return nil, nil
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT v.snapshot FROM stock_auto_configs c JOIN stock_auto_config_audits v USING(tenant_id,moysklad_account,ozon_account,revision) WHERE c.tenant_id=$1 AND c.moysklad_account=$2 AND c.ozon_account=$3 AND c.enabled AND v.enabled AND c.revision=$4 FOR SHARE OF c`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, a.AutomaticPolicyRevision).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p domain.AutomaticStockPolicy
	if err = json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.StockSyncScope != scope || !p.Enabled || p.Revision != a.AutomaticPolicyRevision || p.CreatedAt.After(now) || !p.ValidUntil.After(now) || p.ValidUntil.Sub(p.CreatedAt) > 24*time.Hour || p.EvidenceClass != "OPERATOR_ATTESTED" {
		return nil, nil
	}
	for _, target := range p.Targets {
		if target.ProductID == a.ProductID && target.WarehouseID == a.WarehouseID && syncHash(automaticAttestation(p, target)) == syncHash(a) {
			copy := target
			return &copy, nil
		}
	}
	return nil, nil
}

// authorizedAutomaticReview binds a real test write to the current immutable
// operator policy, exact reviewed mapping revisions and bounded quantity.
// It is deliberately separate from the legacy single-target manual exception.
func authorizedAutomaticReview(ctx context.Context, tx pgx.Tx, review domain.StockSyncReview, now time.Time) (bool, error) {
	if !testpublication.Enabled(review.StockSyncScope) || len(review.Targets) != 1 || len(review.Attestations) != 1 {
		return false, nil
	}
	a, t := review.Attestations[0], review.Targets[0]
	approved, err := authorizedAutomaticAttestation(ctx, tx, review.StockSyncScope, a, now)
	if err != nil || approved == nil || t.Quantity == nil {
		return false, err
	}
	p, w := t.Decision.Evidence.ProductMapping, t.Decision.Evidence.WarehouseMapping
	if p == nil || w == nil || approved.ProductID != t.ProductID || approved.WarehouseID != t.WarehouseID || approved.SourceItemID != t.SourceItemID || approved.SourceWarehouseID != t.SourceWarehouseID || p.ID != approved.ProductMappingID || p.Version != approved.ProductMappingVersion || w.ID != approved.WarehouseMappingID || w.Version != approved.WarehouseMappingVersion || !testpublication.AutomaticQuantityAllowed(*t.Quantity) {
		return false, nil
	}
	quantity, err := strconv.ParseInt(*t.Quantity, 10, 64)
	return err == nil && quantity <= approved.Maximum, nil
}

func loadAutomaticPolicy(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope) (domain.AutomaticStockPolicy, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT v.snapshot FROM stock_auto_configs c JOIN stock_auto_config_audits v USING(tenant_id,moysklad_account,ozon_account,revision) WHERE c.tenant_id=$1 AND c.moysklad_account=$2 AND c.ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&raw)
	if err != nil {
		return domain.AutomaticStockPolicy{}, err
	}
	var p domain.AutomaticStockPolicy
	err = json.Unmarshal(raw, &p)
	return p, err
}

func (s *Store) ConfigureAutomaticStock(ctx context.Context, req domain.AutomaticStockRequest, now time.Time) (domain.AutomaticStockView, error) {
	scope := req.StockSyncScope
	session, merchant := identity.SessionFromContext(ctx)
	allowed := automaticScopeAllowed(scope)
	if !scope.Valid() || (!allowed && (!merchant || req.Enabled)) || (merchant && req.SessionID != session.AuditID) || !req.Acknowledge || req.SessionID == "" || len(req.SessionID) > 128 || len(req.IdempotencyKey) < 16 || len(req.IdempotencyKey) > 128 {
		return domain.AutomaticStockView{}, domain.ErrStockSyncInvalid
	}
	draft := !req.Enabled && (len(req.Targets) > 0 || req.ProductID != "" || req.WarehouseID != "")
	configure := req.Enabled || draft
	if configure && (req.IntervalSeconds < 30 || req.IntervalSeconds > 3600 || req.ValidMinutes < 1 || req.ValidMinutes > 1440) {
		return domain.AutomaticStockView{}, domain.ErrStockSyncInvalid
	}
	requestedTargets := req.Targets
	if len(req.Targets) > 0 && (req.ProductID != "" || req.WarehouseID != "" || req.SafetyStock != "" || req.Unreflected != "" || req.UnseenBound != "" || req.Maximum != 0) {
		return domain.AutomaticStockView{}, domain.ErrStockSyncInvalid
	}
	if configure && len(requestedTargets) == 0 {
		requestedTargets = []domain.AutomaticStockTargetRequest{{ProductID: req.ProductID, WarehouseID: req.WarehouseID, SafetyStock: req.SafetyStock, Unreflected: req.Unreflected, UnseenBound: req.UnseenBound, Maximum: req.Maximum}}
	}
	if configure {
		if len(requestedTargets) == 0 || len(requestedTargets) > 100 {
			return domain.AutomaticStockView{}, domain.ErrStockSyncInvalid
		}
		seen := map[string]bool{}
		for _, t := range requestedTargets {
			key := t.ProductID + ":" + t.WarehouseID
			if seen[key] || !positiveAPIID(t.ProductID) || !positiveAPIID(t.WarehouseID) || t.Maximum < 0 || t.Maximum > testpublication.MaxAutomaticQuantity || !syncQuantity(&t.SafetyStock) || !syncQuantity(&t.Unreflected) || !syncQuantity(&t.UnseenBound) {
				return domain.AutomaticStockView{}, domain.ErrStockSyncInvalid
			}
			seen[key] = true
		}
	}
	now = now.UTC().Truncate(time.Microsecond)
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.AutomaticStockView{}, err
	}
	defer rollbackTransaction(tx)
	if err = syncLock(ctx, tx, scope); err != nil {
		return domain.AutomaticStockView{}, err
	}
	requestHash := syncHash(req)
	var oldHash string
	err = tx.QueryRow(ctx, `SELECT request_hash FROM stock_auto_config_audits WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND idempotency_key=$4`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, req.IdempotencyKey).Scan(&oldHash)
	if err == nil {
		if oldHash != requestHash {
			return domain.AutomaticStockView{}, domain.ErrStockSyncConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.AutomaticStockView{}, err
		}
		return s.AutomaticStockStatus(ctx, scope)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.AutomaticStockView{}, err
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT count(*)=2 FROM stock_connections WHERE tenant_id=$1 AND ((source='moysklad' AND account_id=$2) OR (source='ozon' AND account_id=$3))`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&owned); err != nil {
		return domain.AutomaticStockView{}, err
	}
	if !owned {
		return domain.AutomaticStockView{}, domain.ErrStockSyncNotFound
	}
	var p domain.AutomaticStockPolicy
	if configure {
		p = domain.AutomaticStockPolicy{StockSyncScope: scope, Enabled: req.Enabled, IntervalSeconds: req.IntervalSeconds, CreatedAt: now, ValidUntil: now.Add(time.Duration(req.ValidMinutes) * time.Minute), EvidenceClass: "OPERATOR_ATTESTED"}
		unfenced := 0
		for _, selected := range requestedTargets {
			var t domain.AutomaticStockTarget
			var kind string
			t.ProductID, t.WarehouseID = selected.ProductID, selected.WarehouseID
			err = tx.QueryRow(ctx, `SELECT pm.id,pm.version,pm.target_kind,pm.target_id,wm.id,wm.version,wm.moysklad_warehouse_id FROM product_mappings pm JOIN warehouse_mappings wm ON wm.tenant_id=pm.tenant_id AND wm.moysklad_account=pm.moysklad_account AND wm.ozon_account=pm.ozon_account WHERE pm.tenant_id=$1 AND pm.moysklad_account=$2 AND pm.ozon_account=$3 AND pm.ozon_product_id=$4 AND wm.ozon_warehouse_id=$5 AND wm.status='active' AND EXISTS(SELECT 1 FROM product_mapping_confirmation_audits a WHERE a.mapping_id=pm.id AND a.mapping_version=pm.version) AND EXISTS(SELECT 1 FROM warehouse_mapping_confirmation_audits a WHERE a.mapping_id=wm.id AND a.mapping_version=wm.version) FOR SHARE OF pm,wm`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, t.ProductID, t.WarehouseID).Scan(&t.ProductMappingID, &t.ProductMappingVersion, &kind, &t.SourceItemID, &t.WarehouseMappingID, &t.WarehouseMappingVersion, &t.SourceWarehouseID)
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.AutomaticStockView{}, domain.ErrStockSyncBlocked
			}
			if err != nil {
				return domain.AutomaticStockView{}, err
			}
			if kind != "product" && kind != "variant" {
				return domain.AutomaticStockView{}, domain.ErrStockSyncBlocked
			}
			var fenced bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_publication_target_fences WHERE ozon_account=$1 AND product_id=$2 AND warehouse_id=$3)`, scope.OzonAccount, t.ProductID, t.WarehouseID).Scan(&fenced); err != nil {
				return domain.AutomaticStockView{}, err
			}
			if !fenced {
				unfenced++
			}
			t.SafetyStock, t.Unreflected, t.UnseenBound, t.Maximum = selected.SafetyStock, selected.Unreflected, selected.UnseenBound, selected.Maximum
			t.InitialPublication = selected.InitialPublication
			p.Targets = append(p.Targets, t)
		}
		if req.Enabled && unfenced == 0 {
			return domain.AutomaticStockView{}, domain.ErrStockSyncBlocked
		}
	} else {
		p, err = loadAutomaticPolicy(ctx, tx, scope)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AutomaticStockView{}, domain.ErrStockSyncNotFound
		}
		if err != nil {
			return domain.AutomaticStockView{}, err
		}
		p.Enabled, p.CreatedAt = false, now
	}
	_, err = tx.Exec(ctx, `INSERT INTO stock_sync_settings(tenant_id,moysklad_account,ozon_account,enabled,updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET enabled=EXCLUDED.enabled,review_id=NULL,updated_at=EXCLUDED.updated_at`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, req.Enabled, now)
	if err != nil {
		return domain.AutomaticStockView{}, err
	}
	var revision int64
	err = tx.QueryRow(ctx, `INSERT INTO stock_auto_configs(tenant_id,moysklad_account,ozon_account,revision,enabled,next_run_at) VALUES($1,$2,$3,1,$4,CASE WHEN $4 THEN $5::timestamptz ELSE NULL END) ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET revision=stock_auto_configs.revision+1,enabled=EXCLUDED.enabled,next_run_at=EXCLUDED.next_run_at RETURNING revision`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, req.Enabled, now).Scan(&revision)
	if err != nil {
		return domain.AutomaticStockView{}, err
	}
	p.Revision = revision
	raw, err := json.Marshal(p)
	if err != nil {
		return domain.AutomaticStockView{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stock_auto_config_audits(tenant_id,moysklad_account,ozon_account,revision,enabled,snapshot,principal_type,session_audit_id,idempotency_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$11,$7,$8,$9,$10)`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, revision, req.Enabled, raw, req.SessionID, req.IdempotencyKey, requestHash, now, auditPrincipal(req.SessionID)); err != nil {
		return domain.AutomaticStockView{}, err
	}
	// A changed or paused configuration invalidates unclaimed decisions, but it
	// cannot retract a request that has already left a worker.
	if _, err = tx.Exec(ctx, `UPDATE stock_auto_cycles SET state='CANCELLED',outcome='CONFIGURATION_CHANGED',finished_at=$4 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND state IN ('STARTING','READING','WAITING_PUBLICATION')`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now); err != nil {
		return domain.AutomaticStockView{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_publication_targets t SET state='CANCELLED',safe_error='SYNC_DISABLED',finished_at=$4 FROM stock_publications p WHERE t.operation_id=p.id AND p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND t.state='QUEUED'`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now); err != nil {
		return domain.AutomaticStockView{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE outbox_tasks q SET state='cancelled',last_error='SYNC_DISABLED',updated_at=$4 FROM stock_publications p WHERE q.publication_operation_id=p.id AND p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND q.state='pending'`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now); err != nil {
		return domain.AutomaticStockView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AutomaticStockView{}, err
	}
	return s.AutomaticStockStatus(ctx, scope)
}

func (s *Store) AutomaticStockStatus(ctx context.Context, scope domain.StockSyncScope) (domain.AutomaticStockView, error) {
	_, merchant := identity.SessionFromContext(ctx)
	allowed := automaticScopeAllowed(scope)
	view := domain.AutomaticStockView{Available: allowed || (merchant && scope.Valid()), PublicationAvailable: allowed}
	if !view.Available {
		return view, nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	rows, err := s.Pool.Query(ctx, `SELECT pm.ozon_product_id,wm.ozon_warehouse_id,pm.ozon_offer_id,pm.target_id,(p.product_id IS NOT NULL AND stock.product_id IS NOT NULL AND wh.warehouse_id IS NOT NULL),
		(p.product_id IS NOT NULL AND wh.warehouse_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM ozon_fbs_stock_observations x WHERE x.import_id=p.import_id AND x.product_id=p.product_id AND x.warehouse_id=wm.ozon_warehouse_id)
		 AND NOT EXISTS(SELECT 1 FROM stock_publication_target_fences f WHERE f.ozon_account=pm.ozon_account AND f.product_id=pm.ozon_product_id AND f.warehouse_id=wm.ozon_warehouse_id)
		 AND NOT EXISTS(SELECT 1 FROM stock_publication_targets t WHERE t.ozon_account=pm.ozon_account AND t.product_id=pm.ozon_product_id AND t.warehouse_id=wm.ozon_warehouse_id AND t.dispatched_at IS NOT NULL)),src.stock::text,src.reserve::text,stock.free_stock,last.operation_id,last.state,last.quantity,last.readback_quantity,last.readback_at
		FROM product_mappings pm JOIN warehouse_mappings wm ON wm.tenant_id=pm.tenant_id AND wm.moysklad_account=pm.moysklad_account AND wm.ozon_account=pm.ozon_account AND wm.status='active'
		LEFT JOIN ozon_product_observations p ON p.import_id=(SELECT max(id) FROM source_imports WHERE source='ozon' AND account_id=$3) AND p.product_id=pm.ozon_product_id AND p.offer_id=pm.ozon_offer_id AND p.archived=false AND p.sku IS NOT NULL
		LEFT JOIN ozon_fbs_stock_observations stock ON stock.import_id=p.import_id AND stock.product_id=p.product_id AND stock.offer_id=p.offer_id AND stock.sku=p.sku AND stock.warehouse_id=wm.ozon_warehouse_id
		LEFT JOIN ozon_warehouse_observations wh ON wh.import_id=p.import_id AND wh.warehouse_id=wm.ozon_warehouse_id AND wh.is_rfbs=false AND wh.status='created'
		LEFT JOIN source_warehouse_stock_observations src ON src.import_id=(SELECT max(id) FROM source_imports WHERE source='moysklad' AND account_id=$2) AND src.kind=pm.target_kind AND src.external_id=pm.target_id AND src.warehouse_id=wm.moysklad_warehouse_id
		LEFT JOIN LATERAL (SELECT t.operation_id,t.state,t.quantity,t.readback_quantity,t.readback_at FROM stock_publications pub JOIN stock_publication_targets t ON t.operation_id=pub.id WHERE pub.tenant_id=$1 AND pub.moysklad_account=$2 AND pub.ozon_account=$3 AND t.product_id=pm.ozon_product_id AND t.warehouse_id=wm.ozon_warehouse_id ORDER BY pub.created_at DESC,t.id DESC LIMIT 1) last ON true
		WHERE pm.tenant_id=$1 AND pm.moysklad_account=$2 AND pm.ozon_account=$3 ORDER BY pm.ozon_product_id,wm.ozon_warehouse_id LIMIT 100`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount)
	if err != nil {
		return view, err
	}
	for rows.Next() {
		var c domain.AutomaticStockCandidate
		if err = rows.Scan(&c.ProductID, &c.WarehouseID, &c.OfferID, &c.SourceItemID, &c.Eligible, &c.InitialEligible, &c.SourcePhysical, &c.SourceReserve, &c.MarketplaceFree, &c.LastOperationID, &c.LastState, &c.LastQuantity, &c.ReadbackQuantity, &c.ReadbackAt); err != nil {
			rows.Close()
			return view, err
		}
		view.Candidates = append(view.Candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return view, err
	}
	var raw []byte
	err = s.Pool.QueryRow(ctx, `SELECT v.snapshot,c.next_run_at,c.last_cycle_at FROM stock_auto_configs c JOIN stock_auto_config_audits v USING(tenant_id,moysklad_account,ozon_account,revision) WHERE c.tenant_id=$1 AND c.moysklad_account=$2 AND c.ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&raw, &view.NextRunAt, &view.LastCycleAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	var policy domain.AutomaticStockPolicy
	if err = json.Unmarshal(raw, &policy); err != nil {
		return view, err
	}
	view.Policy = &policy
	var cycle domain.AutomaticStockCycle
	var metrics []byte
	err = s.Pool.QueryRow(ctx, `SELECT id,config_revision,state,outcome,started_at,finished_at,COALESCE(moysklad_import_id,''),COALESCE(ozon_import_id,''),COALESCE(publication_id,''),reconcile_attempts,metrics FROM stock_auto_cycles WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 ORDER BY started_at DESC,id DESC LIMIT 1`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&cycle.ID, &cycle.ConfigRevision, &cycle.State, &cycle.Outcome, &cycle.StartedAt, &cycle.FinishedAt, &cycle.MoyskladImportID, &cycle.OzonImportID, &cycle.PublicationID, &cycle.ReconcileAttempts, &metrics)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return view, err
	}
	if err == nil {
		if err = json.Unmarshal(metrics, &cycle.Metrics); err != nil {
			return view, err
		}
		view.LastCycle = &cycle
	}
	rows, err = s.Pool.Query(ctx, `SELECT t.operation_id,t.product_id,t.warehouse_id,t.quantity,t.state,t.safe_error,t.dispatched_at,t.readback_at,t.readback_quantity FROM stock_publication_targets t JOIN stock_publication_target_fences f ON f.target_id=t.id JOIN stock_publications p ON p.id=t.operation_id WHERE p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND t.state IN ('UNKNOWN','ACKNOWLEDGED') ORDER BY t.id LIMIT 100`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount)
	if err != nil {
		return view, err
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.AutomaticStockUnresolved
		if err = rows.Scan(&item.OperationID, &item.ProductID, &item.WarehouseID, &item.Quantity, &item.State, &item.SafeError, &item.DispatchedAt, &item.ReadbackAt, &item.ReadbackQuantity); err != nil {
			return view, err
		}
		view.Unresolved = append(view.Unresolved, item)
	}
	if err = rows.Err(); err != nil {
		return view, err
	}
	return view, nil
}

// RequestAutomaticCycle advances the next due time only. It never performs a
// provider request or removes a target fence.
func (s *Store) RequestAutomaticCycle(ctx context.Context, scope domain.StockSyncScope, now time.Time) (domain.AutomaticStockView, error) {
	if !automaticScopeAllowed(scope) {
		return domain.AutomaticStockView{}, domain.ErrStockSyncInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.AutomaticStockView{}, err
	}
	defer rollbackTransaction(tx)
	if err = requireContextActor(ctx, tx, scope.Tenant, false); err != nil {
		return domain.AutomaticStockView{}, err
	}
	if err = syncLock(ctx, tx, scope); err != nil {
		return domain.AutomaticStockView{}, err
	}
	p, err := loadAutomaticPolicy(ctx, tx, scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AutomaticStockView{}, domain.ErrStockSyncNotFound
	}
	if err != nil {
		return domain.AutomaticStockView{}, err
	}
	if !p.Enabled || !p.ValidUntil.After(now) {
		return domain.AutomaticStockView{}, domain.ErrStockSyncBlocked
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_auto_configs SET next_run_at=$4 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND enabled AND revision=$5 AND NOT EXISTS(SELECT 1 FROM stock_auto_cycles WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND state IN ('STARTING','READING','WAITING_PUBLICATION'))`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now, p.Revision); err != nil {
		return domain.AutomaticStockView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AutomaticStockView{}, err
	}
	return s.AutomaticStockStatus(ctx, scope)
}
