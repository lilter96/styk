package console

import (
	"net/http"
	"strconv"

	"github.com/lilter96/styk/internal/matching"
	"github.com/lilter96/styk/internal/postgres"
)

func registerProductMatchingJobs(mux *http.ServeMux, a *auth, db *postgres.Store) {
	mux.HandleFunc("POST /api/product-matching/jobs", a.authorized(a.connectionMutation(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			catalogRequestScope
			Criterion matching.Criterion `json:"criterion"`
			Key       string             `json:"idempotency_key"`
		}
		if !decodeStockSync(w, r, &body) {
			return
		}
		j, err := db.CreateProductMatchingJob(r.Context(), body.catalogScope(), catalogActor(r), body.Key, body.Criterion)
		if err != nil {
			catalogHTTPError(w, err)
			return
		}
		writeJSON(w, j)
	})))
	mux.HandleFunc("POST /api/product-matching/jobs/{id}/cancel", a.authorized(a.connectionMutation(func(w http.ResponseWriter, r *http.Request) {
		var body catalogRequestScope
		if !decodeStockSync(w, r, &body) {
			return
		}
		if err := db.CancelProductMatchingJob(r.Context(), body.catalogScope(), r.PathValue("id"), catalogActor(r)); err != nil {
			catalogHTTPError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))

	registerProductMatchingRead(mux, a, db)
	registerProductMatchingApply(mux, a, db)
}

func registerProductMatchingRead(mux *http.ServeMux, a *auth, db *postgres.Store) {
	mux.HandleFunc("POST /api/product-matching/jobs/{id}/retry", a.authorized(a.connectionMutation(func(w http.ResponseWriter, r *http.Request) {
		var body catalogRequestScope
		if !decodeStockSync(w, r, &body) {
			return
		}
		if err := db.RetryProductMatchingJob(r.Context(), body.catalogScope(), r.PathValue("id"), catalogActor(r)); err != nil {
			catalogHTTPError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("GET /api/product-matching/jobs/{id}", a.authorized(func(w http.ResponseWriter, r *http.Request) {
		page := -1
		var err error
		if raw := r.URL.Query().Get("page"); raw != "" {
			page, err = strconv.Atoi(raw)
		}
		if err != nil || page < -1 {
			writeError(w, 400, "INVALID_PAGE")
			return
		}
		j, err := db.ProductMatchingJob(r.Context(), catalogScope(r), r.PathValue("id"), catalogActor(r), page)
		if err != nil {
			catalogHTTPError(w, err)
			return
		}
		writeJSON(w, j)
	}))
}

func registerProductMatchingApply(mux *http.ServeMux, a *auth, db *postgres.Store) {
	mux.HandleFunc("POST /api/product-matching/jobs/{id}/apply", a.authorized(a.connectionMutation(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			catalogRequestScope
			Key         string `json:"idempotency_key"`
			Expected    int    `json:"expected_count"`
			Acknowledge bool   `json:"acknowledge"`
		}
		if !decodeStockSync(w, r, &body) {
			return
		}
		req := postgres.ProductMatchingApproval{Scope: body.catalogScope(), JobID: r.PathValue("id"), Actor: catalogActor(r), Key: body.Key, Expected: body.Expected, Acknowledge: body.Acknowledge}
		if err := db.ApplyProductMatchingJob(r.Context(), req); err != nil {
			catalogHTTPError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]string{"job_id": r.PathValue("id")})
	})))
}
