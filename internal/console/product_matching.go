package console

import (
	"net/http"

	"github.com/lilter96/styk/internal/matching"
	"github.com/lilter96/styk/internal/postgres"
)

func registerProductMatchingRoutes(mux *http.ServeMux, a *auth, db *postgres.Store) {
	registerProductMatchingSettings(mux, a, db)
	registerProductMatchingJobs(mux, a, db)
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
