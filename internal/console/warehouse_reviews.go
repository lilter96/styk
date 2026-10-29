package console

import (
	"encoding/json"
	"errors"
	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

func warehouseReviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrReviewConflict):
		writeError(w, 409, "WAREHOUSE_ALREADY_ASSIGNED")
	case errors.Is(err, postgres.ErrReviewScope), errors.Is(err, postgres.ErrReviewSource), errors.Is(err, postgres.ErrReviewMissing):
		writeError(w, 404, "REVIEW_SOURCE_NOT_FOUND")
	case errors.Is(err, postgres.ErrReviewEvidence):
		writeError(w, 400, "INVALID_REVIEW")
	default:
		writeError(w, 503, "REVIEW_UNAVAILABLE")
	}
}
func (a *auth) createWarehouseReview(db *postgres.Store, lifetime time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if r.Header.Get("Origin") != a.origin {
			writeError(w, 403, "FORBIDDEN")
			return
		}
		if err != nil || media != "application/json" {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		var body struct {
			Tenant string `json:"tenant"`
			Moy    string `json:"moysklad_account"`
			Ozon   string `json:"ozon_account"`
			MoyID  string `json:"moysklad_warehouse_id"`
			OzonID string `json:"ozon_warehouse_id"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if dec.Decode(&body) != nil || dec.Decode(&struct{}{}) != io.EOF {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		for _, v := range []string{body.Tenant, body.Moy, body.Ozon, body.MoyID, body.OzonID} {
			if v == "" || strings.TrimSpace(v) != v || len(v) > 256 {
				writeError(w, 400, "INVALID_REQUEST")
				return
			}
		}
		audit, _ := r.Context().Value(auditContextKey{}).(string)
		if audit == "" {
			writeError(w, 401, "UNAUTHORIZED")
			return
		}
		result, err := (application.WarehouseReviews{Store: db}).Create(r.Context(), domain.WarehouseReviewRequest{Tenant: body.Tenant, MoyskladAccount: body.Moy, OzonAccount: body.Ozon, MoyskladWarehouseID: body.MoyID, OzonWarehouseID: body.OzonID, SessionID: audit, Lifetime: lifetime})
		if err != nil {
			warehouseReviewError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		writeJSON(w, result)
	}
}
func (a *auth) inspectWarehouseReview(db *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if len(id) != 32 {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		audit, _ := r.Context().Value(auditContextKey{}).(string)
		if audit == "" {
			writeError(w, 401, "UNAUTHORIZED")
			return
		}
		result, err := (application.WarehouseReviews{Store: db}).Inspect(r.Context(), id, audit)
		if err != nil {
			warehouseReviewError(w, err)
			return
		}
		writeJSON(w, result)
	}
}
