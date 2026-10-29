package console

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/stocksync"
	"github.com/lilter96/styk/internal/testpublication"
)

type stockSyncAPI struct {
	db     *postgres.Store
	runner stocksync.Runner
}

func registerStockSyncRoutes(mux *http.ServeMux, a *auth, db *postgres.Store, c *connections) {
	api := &stockSyncAPI{db: db, runner: stocksync.Runner{Store: db, ResolveOzon: c.resolveOzon}}
	mux.HandleFunc("GET /api/stock-sync", a.authorized(api.get))
	mux.HandleFunc("GET /api/stock-sync/eligibility", a.authorized(api.eligibility))
	mux.HandleFunc("GET /api/stock-sync/shadow", a.authorized(api.shadow))
	mux.HandleFunc("POST /api/stock-sync/preview", a.authorized(a.connectionMutation(api.preview)))
	mux.HandleFunc("POST /api/stock-sync/enable", a.authorized(a.connectionMutation(api.enable)))
	mux.HandleFunc("POST /api/stock-sync/disable", a.authorized(a.connectionMutation(api.disable)))
	mux.HandleFunc("POST /api/stock-sync/operations/{id}/reconcile", a.authorized(a.connectionMutation(api.reconcile)))
	mux.HandleFunc("POST /api/stock-sync/operations/{id}/supersede", a.authorized(a.connectionMutation(api.supersede)))
	mux.HandleFunc("GET /api/stock-sync/automatic", a.authorized(api.automaticStatus))
	mux.HandleFunc("POST /api/stock-sync/automatic", a.authorized(a.connectionMutation(api.automaticConfigure)))
	mux.HandleFunc("POST /api/stock-sync/automatic/run", a.authorized(a.connectionMutation(api.automaticRun)))
}

func (a *stockSyncAPI) eligibility(w http.ResponseWriter, r *http.Request) {
	scope := syncScope(r)
	if !scope.Valid() {
		writeError(w, http.StatusBadRequest, "INVALID_SCOPE")
		return
	}
	var owned bool
	err := a.db.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$2)
	 AND EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='ozon' AND account_id=$3)`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&owned)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	state := "AWAITING_PRODUCTION_ACTIVATION"
	if scope == testpublication.Scope() {
		state = "CONTROLLED_TEST_DISABLED"
		if testpublication.Enabled(scope) {
			state = "CONTROLLED_TEST_AVAILABLE"
		}
	}
	writeJSON(w, struct {
		State string `json:"state"`
	}{state})
}

func (a *stockSyncAPI) automaticStatus(w http.ResponseWriter, r *http.Request) {
	scope := syncScope(r)
	if !scope.Valid() {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	view, err := a.db.AutomaticStockStatus(r.Context(), scope)
	if err != nil {
		stockSyncError(w, err)
		return
	}
	writeJSON(w, view)
}

func (a *stockSyncAPI) automaticConfigure(w http.ResponseWriter, r *http.Request) {
	var req domain.AutomaticStockRequest
	if !decodeStockSync(w, r, &req) {
		return
	}
	req.SessionID, _ = r.Context().Value(auditContextKey{}).(string)
	if req.SessionID == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	view, err := a.db.ConfigureAutomaticStock(r.Context(), req, time.Now().UTC())
	if err != nil {
		stockSyncError(w, err)
		return
	}
	writeJSON(w, view)
}

func (a *stockSyncAPI) automaticRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		domain.StockSyncScope
		Acknowledge bool `json:"acknowledge"`
	}
	if !decodeStockSync(w, r, &req) {
		return
	}
	if !req.Acknowledge {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	view, err := a.db.RequestAutomaticCycle(r.Context(), req.StockSyncScope, time.Now().UTC())
	if err != nil {
		stockSyncError(w, err)
		return
	}
	writeJSON(w, view)
}

// shadow exposes only a bounded projection of the existing status calculation.
// It neither persists a review nor talks to a marketplace.
func (a *stockSyncAPI) shadow(w http.ResponseWriter, r *http.Request) {
	scope := syncScope(r)
	if !scope.Valid() {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	now := time.Now().UTC()
	view, err := a.db.StockSyncStatus(r.Context(), scope, now)
	if err != nil {
		stockSyncError(w, err)
		return
	}
	type target struct {
		ProductID                 string                  `json:"product_id"`
		OfferID                   string                  `json:"offer_id"`
		SKU                       string                  `json:"sku"`
		WarehouseID               string                  `json:"warehouse_id"`
		WarehouseName             string                  `json:"warehouse_name"`
		SourceItemID              string                  `json:"source_item_id"`
		SourceItemName            string                  `json:"source_item_name"`
		SourceParentID            string                  `json:"source_parent_id"`
		SourceParentName          string                  `json:"source_parent_name"`
		SourceWarehouseID         string                  `json:"source_warehouse_id"`
		SourceWarehouseName       string                  `json:"source_warehouse_name"`
		ProductMappingConfirmed   bool                    `json:"product_mapping_confirmed"`
		WarehouseMappingConfirmed bool                    `json:"warehouse_mapping_confirmed"`
		UncertainWrite            bool                    `json:"uncertain_write"`
		HistoricalUncertainWrite  bool                    `json:"historical_uncertain_write"`
		ExpiredEvidence           bool                    `json:"expired_evidence"`
		Physical                  *string                 `json:"physical"`
		Reserve                   *string                 `json:"reserve"`
		Unreflected               *string                 `json:"unreflected"`
		UnseenBound               *string                 `json:"unseen_bound"`
		SafetyStock               *string                 `json:"safety_stock"`
		Candidate                 *string                 `json:"candidate"`
		MarketplaceFree           *string                 `json:"marketplace_free"`
		SourceBatchStarted        *time.Time              `json:"source_batch_started"`
		MarketplaceBatchStarted   *time.Time              `json:"marketplace_batch_started"`
		SourceBatchFinished       *time.Time              `json:"source_batch_finished"`
		MarketplaceBatchFinished  *time.Time              `json:"marketplace_batch_finished"`
		SourceTraversalFinished   *time.Time              `json:"source_traversal_finished,omitempty"`
		MarketTraversalFinished   *time.Time              `json:"marketplace_traversal_finished,omitempty"`
		Checks                    []domain.StockSyncCheck `json:"checks"`
	}
	targets := make([]target, 0, len(view.Targets))
	for _, v := range view.Targets {
		d := v.Decision
		t := target{ProductID: v.ProductID, WarehouseID: v.WarehouseID, SourceItemID: v.SourceItemID, SourceWarehouseID: v.SourceWarehouseID, Physical: d.Evidence.Source.Physical, Reserve: d.Evidence.Source.Reserve, Unreflected: d.Evidence.Unreflected, UnseenBound: d.Evidence.UnseenBound, SourceBatchFinished: d.Evidence.Source.ObservedAt, MarketplaceBatchFinished: d.Evidence.Marketplace.ObservedAt, Checks: v.Checks}
		if d.Evidence.Source.FreshTraversal != nil {
			t.SourceTraversalFinished = &d.Evidence.Source.FreshTraversal.FinishedAt
		}
		if d.Evidence.Marketplace.FreshTraversal != nil {
			t.MarketTraversalFinished = &d.Evidence.Marketplace.FreshTraversal.FinishedAt
		}
		t.ProductMappingConfirmed = d.Evidence.ProductMapping != nil
		t.WarehouseMappingConfirmed = d.Evidence.WarehouseMapping != nil
		if d.Evidence.Unreflected != nil {
			t.SafetyStock = &d.Request.Policy.SafetyStock
		}
		if d.Evidence.ProductMapping != nil {
			t.OfferID = d.Evidence.ProductMapping.OfferID
		}
		// Quantity is set by the authoritative target review only when that
		// target passed its checks. Other targets can still block the whole scope.
		t.Candidate = v.Quantity
		for _, proof := range d.Evidence.Proofs {
			if (proof.State == domain.ProofVerified || proof.State == domain.ProofAttested) && !proof.ValidUntil.IsZero() && !proof.ValidUntil.After(now) {
				t.ExpiredEvidence = true
			}
		}
		if d.Evidence.Marketplace.FreeStock != nil {
			value := strconv.FormatInt(*d.Evidence.Marketplace.FreeStock, 10)
			t.MarketplaceFree = &value
		}
		for _, batch := range []struct {
			id                *int64
			provider, account string
			started           **time.Time
			finished          **time.Time
		}{{d.Evidence.Source.ImportID, "moysklad", scope.MoyskladAccount, &t.SourceBatchStarted, &t.SourceBatchFinished}, {d.Evidence.Marketplace.ImportID, "ozon", scope.OzonAccount, &t.MarketplaceBatchStarted, &t.MarketplaceBatchFinished}} {
			if batch.id == nil {
				continue
			}
			var started, finished time.Time
			if err := a.db.Pool.QueryRow(r.Context(), `SELECT started_at,finished_at FROM source_imports WHERE id=$1 AND source=$2 AND account_id=$3`, *batch.id, batch.provider, batch.account).Scan(&started, &finished); err != nil {
				stockSyncError(w, err)
				return
			}
			*batch.started = &started
			*batch.finished = &finished
		}
		if id := d.Evidence.Source.ImportID; id != nil {
			err = a.db.Pool.QueryRow(r.Context(), `SELECT
			 COALESCE((SELECT name FROM source_entities WHERE import_id=$1 AND kind=$2 AND external_id=$3),''),
			 COALESCE((SELECT parent_id FROM source_entities WHERE import_id=$1 AND kind=$2 AND external_id=$3),''),
			 COALESCE((SELECT p.name FROM source_entities c JOIN source_entities p ON p.import_id=c.import_id AND p.kind='product' AND p.external_id=c.parent_id WHERE c.import_id=$1 AND c.kind=$2 AND c.external_id=$3),''),
			 COALESCE((SELECT name FROM source_entities WHERE import_id=$1 AND kind='store' AND external_id=$4),'')`, *id, d.Request.Pool.ItemKind, v.SourceItemID, v.SourceWarehouseID).Scan(&t.SourceItemName, &t.SourceParentID, &t.SourceParentName, &t.SourceWarehouseName)
			if err != nil {
				stockSyncError(w, err)
				return
			}
		}
		if id := d.Evidence.Marketplace.ImportID; id != nil {
			err = a.db.Pool.QueryRow(r.Context(), `SELECT COALESCE((SELECT sku FROM ozon_product_observations WHERE import_id=$1 AND product_id=$2),''), COALESCE((SELECT name FROM ozon_warehouse_observations WHERE import_id=$1 AND warehouse_id=$3),'')`, *id, v.ProductID, v.WarehouseID).Scan(&t.SKU, &t.WarehouseName)
			if err != nil {
				stockSyncError(w, err)
				return
			}
		}
		// Physical fences apply across scopes for this Ozon account. A recovered
		// UNKNOWN remains history; only its own fence or missing audit leaves it unresolved.
		err = a.db.Pool.QueryRow(r.Context(), `WITH unknowns AS (
		 SELECT p.ozon_account=t.ozon_account AS scope_valid,
		  EXISTS(SELECT 1 FROM stock_publication_target_fences f WHERE f.target_id=t.id) AS fenced,
		  EXISTS(SELECT 1 FROM stock_publication_recoveries r
		   JOIN stock_publication_readbacks b ON b.id=r.readback_id AND b.target_id=t.id
		   WHERE r.target_id=t.id AND r.tenant_id=p.tenant_id AND r.moysklad_account=p.moysklad_account
		    AND r.ozon_account=t.ozon_account AND r.product_id=t.product_id AND r.warehouse_id=t.warehouse_id
		    AND r.accepted_uncertainty AND r.worker_stopped_assertion
		    AND b.quantity=r.observed_quantity AND b.observed_at=r.observed_at AND b.outcome IN ('MATCH','MISMATCH')) AS recovered
		 FROM stock_publication_targets t JOIN stock_publications p ON p.id=t.operation_id
		 WHERE t.ozon_account=$1 AND t.product_id=$2 AND t.warehouse_id=$3 AND t.state='UNKNOWN'
		)
		SELECT COALESCE(bool_or(NOT scope_valid OR fenced OR NOT recovered),false)
		 OR EXISTS(SELECT 1 FROM stock_publication_target_fences f
		  LEFT JOIN stock_publication_targets t ON t.id=f.target_id
		  LEFT JOIN stock_publications p ON p.id=t.operation_id
		  WHERE f.ozon_account=$1 AND f.product_id=$2 AND f.warehouse_id=$3
		   AND (t.id IS NULL OR p.id IS NULL OR t.ozon_account IS DISTINCT FROM f.ozon_account
		    OR t.product_id IS DISTINCT FROM f.product_id OR t.warehouse_id IS DISTINCT FROM f.warehouse_id
		    OR p.ozon_account IS DISTINCT FROM f.ozon_account OR t.state NOT IN ('QUEUED','DISPATCHING','ACKNOWLEDGED','UNKNOWN'))),
		 COALESCE(bool_or(scope_valid AND NOT fenced AND recovered),false) FROM unknowns`, scope.OzonAccount, v.ProductID, v.WarehouseID).Scan(&t.UncertainWrite, &t.HistoricalUncertainWrite)
		if err != nil {
			stockSyncError(w, err)
			return
		}
		targets = append(targets, t)
	}
	writeJSON(w, map[string]any{"scope": scope, "checks": view.Checks, "state": view.State, "targets": targets, "publication_available": testpublication.Enabled(scope), "test_account_mode": testpublication.Enabled(scope)})
}
func syncScope(r *http.Request) domain.StockSyncScope {
	q := r.URL.Query()
	return domain.StockSyncScope{Tenant: q.Get("tenant"), MoyskladAccount: q.Get("moysklad_account"), OzonAccount: q.Get("ozon_account")}
}
func stockSyncError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrStockSyncInvalid):
		writeError(w, 400, "INVALID_REQUEST")
	case errors.Is(err, domain.ErrStockSyncNotFound):
		writeError(w, 404, "STOCK_SYNC_NOT_FOUND")
	case errors.Is(err, domain.ErrStockSyncBlocked):
		writeError(w, 409, "STOCK_SYNC_BLOCKED")
	case errors.Is(err, domain.ErrStockSyncStale):
		writeError(w, 409, "STOCK_SYNC_STALE")
	case errors.Is(err, domain.ErrStockSyncConflict):
		writeError(w, 409, "IDEMPOTENCY_CONFLICT")
	default:
		writeError(w, 503, "STOCK_SYNC_UNAVAILABLE")
	}
}
func decodeStockSync(w http.ResponseWriter, r *http.Request, dest any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
	d.DisallowUnknownFields()
	if d.Decode(dest) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "INVALID_REQUEST")
		return false
	}
	return true
}
func (a *stockSyncAPI) get(w http.ResponseWriter, r *http.Request) {
	scope := syncScope(r)
	if !scope.Valid() {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	view, err := a.db.StockSyncStatus(r.Context(), scope, time.Now().UTC())
	if err != nil {
		stockSyncError(w, err)
		return
	}
	writeStockSyncView(w, view)
}
func (a *stockSyncAPI) preview(w http.ResponseWriter, r *http.Request) {
	var req domain.StockSyncPreviewRequest
	if !decodeStockSync(w, r, &req) {
		return
	}
	if !req.StockSyncScope.Valid() {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	review, err := a.db.PreviewStockSync(r.Context(), req, time.Now().UTC())
	if err != nil {
		stockSyncError(w, err)
		return
	}
	writeJSON(w, postgres.StockSyncPublic(review))
}

type stockSyncMutation struct {
	domain.StockSyncScope
	ReviewID    string `json:"review_id,omitempty"`
	Key         string `json:"idempotency_key"`
	Acknowledge bool   `json:"acknowledge"`
}

func (a *stockSyncAPI) enable(w http.ResponseWriter, r *http.Request)  { a.mutate(w, r, "enable") }
func (a *stockSyncAPI) disable(w http.ResponseWriter, r *http.Request) { a.mutate(w, r, "disable") }
func (a *stockSyncAPI) mutate(w http.ResponseWriter, r *http.Request, action string) {
	var req stockSyncMutation
	if !decodeStockSync(w, r, &req) {
		return
	}
	if !req.StockSyncScope.Valid() || !req.Acknowledge || len(req.Key) < 16 || len(req.Key) > 128 || (action == "enable" && len(req.ReviewID) != 32) {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	if action == "enable" {
		if _, ok := config.StockPublicationTestEndpoint(); !ok && !testpublication.Enabled(req.StockSyncScope) {
			writeError(w, http.StatusServiceUnavailable, "STOCK_PUBLICATION_DISABLED")
			return
		}
	}
	view, err := a.db.MutateStockSync(r.Context(), req.StockSyncScope, action, req.ReviewID, req.Key, time.Now().UTC())
	if err != nil {
		stockSyncError(w, err)
		return
	}
	writeStockSyncView(w, view)
}
func (a *stockSyncAPI) reconcile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		domain.StockSyncScope
		Acknowledge bool `json:"acknowledge"`
	}
	if !decodeStockSync(w, r, &req) {
		return
	}
	if !req.StockSyncScope.Valid() || !req.Acknowledge || len(r.PathValue("id")) != 32 {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	view, err := a.runner.Reconcile(r.Context(), req.StockSyncScope, r.PathValue("id"))
	if err != nil {
		stockSyncError(w, err)
		return
	}
	writeStockSyncView(w, view)
}

func (a *stockSyncAPI) supersede(w http.ResponseWriter, r *http.Request) {
	var req domain.StockRecoveryRequest
	if !decodeStockSync(w, r, &req) {
		return
	}
	req.OperationID = r.PathValue("id")
	req.SessionID, _ = r.Context().Value(auditContextKey{}).(string)
	if req.SessionID == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	view, err := a.db.SupersedeTestUnknown(r.Context(), req, time.Now().UTC())
	if err != nil {
		stockSyncError(w, err)
		return
	}
	writeStockSyncView(w, view)
}

// Real publication is available only for the owner's fixed test-account scope.
// The simulator does not expose publication through the operator UI.
func writeStockSyncView(w http.ResponseWriter, view domain.StockSyncView) {
	data, ok := postgres.StockSyncPublic(view).(map[string]any)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "STOCK_SYNC_UNAVAILABLE")
		return
	}
	data["publication_available"] = testpublication.Enabled(view.StockSyncScope)
	data["test_account_mode"] = testpublication.Enabled(view.StockSyncScope)
	writeJSON(w, data)
}
