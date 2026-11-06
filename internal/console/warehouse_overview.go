package console

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/postgres"
)

// Optional views preserve the independent-list contract for existing consumers.
func warehouseView(db *postgres.Store, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope := postgres.MatchScope{Tenant: strings.TrimSpace(q.Get("tenant")), MoyskladAccount: strings.TrimSpace(q.Get("moysklad_account")), OzonAccount: strings.TrimSpace(q.Get("ozon_account"))}
	request := postgres.WarehouseReadRequest{Search: strings.TrimSpace(q.Get("search")), Filter: q.Get("filter"), Warehouse: q.Get("warehouse"), Cursor: q.Get("cursor"), Limit: 25}
	var err error
	if q.Has("offset") {
		request.Offset, err = strconv.Atoi(q.Get("offset"))
	}
	if err != nil || !request.Valid() {
		writeError(w, 400, "INVALID_PAGE")
		return
	}
	var result any
	switch q.Get("view") {
	case "overview":
		result, err = db.WarehouseOverview(r.Context(), scope, request)
	case "candidates":
		result, err = db.WarehouseCandidates(r.Context(), scope, request)
	default:
		writeError(w, 400, "INVALID_VIEW")
		return
	}
	if err != nil {
		warehouseReadError(w, err)
		return
	}
	writeJSON(w, result)
}

func warehouseReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, postgres.ErrReviewScope) {
		writeError(w, 404, "SCOPE_NOT_REGISTERED")
		return
	}
	if errors.Is(err, postgres.ErrReviewEvidence) {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	writeError(w, 503, "WAREHOUSES_UNAVAILABLE")
}
