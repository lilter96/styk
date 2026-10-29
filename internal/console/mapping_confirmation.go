package console

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func (a *auth) confirmMappingReview(db *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != a.origin {
			writeError(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		var body struct {
			IdempotencyKey string `json:"idempotency_key"`
			Acknowledge    *bool  `json:"acknowledge"`
			Explanation    string `json:"explanation"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if dec.Decode(&body) != nil || dec.Decode(&struct{}{}) != io.EOF || body.Acknowledge == nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		audit, _ := r.Context().Value(auditContextKey{}).(string)
		if audit == "" {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
			return
		}
		req := domain.MappingConfirmationRequest{ReviewID: r.PathValue("id"), SessionID: audit, IdempotencyKey: body.IdempotencyKey, Acknowledged: *body.Acknowledge, Explanation: body.Explanation}
		result, err := (application.MappingConfirmation{Store: db}).Confirm(r.Context(), req)
		if err != nil {
			switch {
			case errors.Is(err, domain.ErrInvalidMappingConfirmation):
				writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
			case errors.Is(err, postgres.ErrReviewMissing):
				writeError(w, http.StatusNotFound, "REVIEW_NOT_FOUND")
			case errors.Is(err, postgres.ErrReviewExpired):
				writeError(w, http.StatusConflict, "REVIEW_EXPIRED")
			case errors.Is(err, postgres.ErrReviewStale):
				writeError(w, http.StatusConflict, "REVIEW_STALE")
			case errors.Is(err, postgres.ErrMappingConflict):
				writeError(w, http.StatusConflict, "MAPPING_CONFLICT")
			case errors.Is(err, postgres.ErrIdempotencyConflict):
				writeError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT")
			default:
				writeError(w, http.StatusServiceUnavailable, "PERSISTENCE_FAILURE")
			}
			return
		}
		writeJSON(w, result)
	}
}
