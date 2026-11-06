package console

import (
	"net/http"

	"github.com/lilter96/styk/internal/matching"
	"github.com/lilter96/styk/internal/postgres"
)

func registerProductMatchingRoutes(mux *http.ServeMux, a *auth, db *postgres.Store) {
	registerProductMatchingSettings(mux, a, db)
	registerProductMatchingPreview(mux, a, db)
	registerProductMatchingRuns(mux, a, db)
}

func registerProductMatchingSettings(mux *http.ServeMux, a *auth, db *postgres.Store) {
	mux.HandleFunc("GET /api/product-matching/settings", a.authorized(func(w http.ResponseWriter, r *http.Request) {
		value, err := db.ProductMatchPolicy(r.Context(), catalogScope(r))
		if err != nil {
			catalogHTTPError(w, err)
			return
		}
		writeJSON(w, value)
	}))
	mux.HandleFunc("POST /api/product-matching/settings", a.authorized(a.connectionMutation(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			catalogRequestScope
			Policy matching.Policy `json:"policy"`
		}
		if !decodeStockSync(w, r, &body) {
			return
		}
		value, err := db.SaveProductMatchPolicy(r.Context(), body.catalogScope(), body.Policy, catalogActor(r))
		if err != nil {
			catalogHTTPError(w, err)
			return
		}
		writeJSON(w, value)
	})))
}

func registerProductMatchingPreview(mux *http.ServeMux, a *auth, db *postgres.Store) {
	mux.HandleFunc("POST /api/product-matching/preview", a.authorized(a.connectionMutation(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			catalogRequestScope
			Revision int64 `json:"expected_revision"`
		}
		if !decodeStockSync(w, r, &body) {
			return
		}
		value, err := db.PreviewProductMatches(r.Context(), body.catalogScope(), body.Revision, catalogActor(r))
		if err != nil {
			catalogHTTPError(w, err)
			return
		}
		writeJSON(w, value)
	})))
}

func registerProductMatchingRuns(mux *http.ServeMux, a *auth, db *postgres.Store) {
	mux.HandleFunc("POST /api/product-matching/runs", a.authorized(a.connectionMutation(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			catalogRequestScope
			PreviewID   string   `json:"preview_id"`
			Products    []string `json:"product_ids"`
			Key         string   `json:"idempotency_key"`
			Acknowledge bool     `json:"acknowledge"`
		}
		if !decodeStockSync(w, r, &body) {
			return
		}
		value, err := db.ExecuteProductMatches(r.Context(), postgres.ProductMatchExecute{Scope: body.catalogScope(), PreviewID: body.PreviewID, Products: body.Products, IdempotencyKey: body.Key, Acknowledge: body.Acknowledge, Actor: catalogActor(r)})
		if err != nil {
			catalogHTTPError(w, err)
			return
		}
		writeJSON(w, value)
	})))
	mux.HandleFunc("GET /api/product-matching/runs/{id}", a.authorized(func(w http.ResponseWriter, r *http.Request) {
		value, err := db.ProductMatchRun(r.Context(), catalogScope(r), r.PathValue("id"))
		if err != nil {
			catalogHTTPError(w, err)
			return
		}
		writeJSON(w, value)
	}))
}
