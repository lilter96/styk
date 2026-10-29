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
	mux.HandleFunc("GET /api/stock-sync/shadow", a.authorized(api.shadow))
	mux.HandleFunc("POST /api/stock-sync/preview", a.authorized(a.connectionMutation(api.preview)))
	mux.HandleFunc("POST /api/stock-sync/enable", a.authorized(a.connectionMutation(api.enable)))
	mux.HandleFunc("POST /api/stock-sync/disable", a.authorized(a.connectionMutation(api.disable)))
	mux.HandleFunc("POST /api/stock-sync/operations/{id}/reconcile", a.authorized(a.connectionMutation(api.reconcile)))
	mux.HandleFunc("POST /api/stock-sync/operations/{id}/supersede", a.authorized(a.connectionMutation(api.supersede)))
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
			if proof.State == domain.ProofVerified && !proof.ValidUntil.IsZero() && !proof.ValidUntil.After(now) {
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
		// An uncertain physical target fences all scopes for this Ozon account.
		err = a.db.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM stock_publication_targets t JOIN stock_publications p ON p.id=t.operation_id WHERE p.ozon_account=$1 AND t.product_id=$2 AND t.warehouse_id=$3 AND t.state='UNKNOWN')`, scope.OzonAccount, v.ProductID, v.WarehouseID).Scan(&t.UncertainWrite)
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
