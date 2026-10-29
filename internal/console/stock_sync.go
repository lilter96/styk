package console

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/stocksync"
)

type stockSyncAPI struct {
	db     *postgres.Store
	runner stocksync.Runner
}

func registerStockSyncRoutes(mux *http.ServeMux, a *auth, db *postgres.Store, c *connections) {
	api := &stockSyncAPI{db: db, runner: stocksync.Runner{Store: db, ResolveOzon: c.resolveOzon}}
	mux.HandleFunc("GET /api/stock-sync", a.authorized(api.get))
	mux.HandleFunc("POST /api/stock-sync/preview", a.authorized(a.connectionMutation(api.preview)))
	mux.HandleFunc("POST /api/stock-sync/enable", a.authorized(a.connectionMutation(api.enable)))
	mux.HandleFunc("POST /api/stock-sync/disable", a.authorized(a.connectionMutation(api.disable)))
	mux.HandleFunc("POST /api/stock-sync/operations/{id}/reconcile", a.authorized(a.connectionMutation(api.reconcile)))
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
	writeJSON(w, postgres.StockSyncPublic(view))
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
	view, err := a.db.MutateStockSync(r.Context(), req.StockSyncScope, action, req.ReviewID, req.Key, time.Now().UTC())
	if err != nil {
		stockSyncError(w, err)
		return
	}
	writeJSON(w, postgres.StockSyncPublic(view))
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
	writeJSON(w, postgres.StockSyncPublic(view))
}
