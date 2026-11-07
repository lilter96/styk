package console

import (
	"net/http"
	"strconv"

	"github.com/lilter96/styk/internal/postgres"
)

func registerProductMatchingJobs(mux *http.ServeMux, a *auth, db *postgres.Store) {
	mux.HandleFunc("POST /api/product-matching/jobs", a.authorized(a.connectionMutation(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			catalogRequestScope
			Revision int64  `json:"expected_revision"`
			Key      string `json:"idempotency_key"`
		}
		if !decodeStockSync(w, r, &body) {
			return
		}
		j, err := db.CreateProductMatchingJob(r.Context(), body.catalogScope(), body.Revision, catalogActor(r), body.Key)
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
		page := 0
		var err error
		if raw := r.URL.Query().Get("page"); raw != "" {
			page, err = strconv.Atoi(raw)
		}
		if err != nil || page < 0 {
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
