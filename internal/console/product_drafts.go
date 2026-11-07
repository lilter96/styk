package console

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/lilter96/styk/internal/postgres"
)

func registerProductDraftRoutes(mux *http.ServeMux, a *auth, db *postgres.Store) {
	mux.HandleFunc("GET /api/product-import-draft", a.authorized(func(w http.ResponseWriter, r *http.Request) {
		value, err := db.ProductImportDraft(r.Context(), catalogScope(r), r.URL.Query().Get("provider"))
		if err != nil {
			catalogHTTPError(w, err)
			return
		}
		writeJSON(w, value)
	}))
	mux.HandleFunc("POST /api/product-import-draft", a.authorized(a.connectionMutation(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			catalogRequestScope
			Provider string          `json:"provider"`
			Revision int64           `json:"revision"`
			State    json.RawMessage `json:"state"`
			Key      string          `json:"idempotency_key"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 9<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		value, err := db.SaveProductImportDraft(r.Context(), postgres.ProductDraftSave{Scope: body.catalogScope(), Provider: body.Provider, Revision: body.Revision, State: body.State, Key: body.Key, Actor: catalogActor(r)})
		if err != nil {
			catalogHTTPError(w, err)
			return
		}
		writeJSON(w, value)
	})))
}
