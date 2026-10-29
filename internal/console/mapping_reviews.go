package console

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func reviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrReviewScope), errors.Is(err, postgres.ErrReviewSource), errors.Is(err, postgres.ErrReviewMissing):
		writeError(w, http.StatusNotFound, "REVIEW_SOURCE_NOT_FOUND")
	case errors.Is(err, postgres.ErrReviewEvidence):
		writeError(w, http.StatusBadRequest, "INVALID_REVIEW")
	default:
		writeError(w, http.StatusServiceUnavailable, "REVIEW_UNAVAILABLE")
	}
}
func (a *auth) createMappingReview(db *postgres.Store, lifetime time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.sameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeError(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		var body struct {
			Tenant   string                      `json:"tenant"`
			Ozon     string                      `json:"ozon_account"`
			Moy      string                      `json:"moysklad_account"`
			Product  string                      `json:"product_id"`
			Kind     string                      `json:"target_kind"`
			Target   string                      `json:"target_id"`
			Evidence domain.MappingEvidenceClass `json:"evidence_class"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if dec.Decode(&body) != nil {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		if dec.Decode(&struct{}{}) != io.EOF {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		for _, v := range []string{body.Tenant, body.Ozon, body.Moy, body.Product, body.Kind, body.Target} {
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
		result, err := (application.MappingReviews{Store: db}).Create(r.Context(), domain.MappingReviewRequest{Tenant: body.Tenant, OzonAccount: body.Ozon, MoyskladAccount: body.Moy, ProductID: body.Product, TargetKind: body.Kind, TargetID: body.Target, Evidence: body.Evidence, SessionID: audit, Lifetime: lifetime})
		if err != nil {
			reviewError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, result)
	}
}
func (a *auth) inspectMappingReview(db *postgres.Store) http.HandlerFunc {
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
		result, err := (application.MappingReviews{Store: db}).Inspect(r.Context(), id, audit)
		if err != nil {
			reviewError(w, err)
			return
		}
		writeJSON(w, result)
	}
}
