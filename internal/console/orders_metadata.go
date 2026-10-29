package console

import "net/http"

// Registry reads only account-scoped, reconciled native attribute identities.
// It neither creates metadata nor treats an absent provider field as zero.
func (a *ordersAPI) metadataRegistry(w http.ResponseWriter, r *http.Request) {
	scope := orderScope(r)
	if _, err := a.service.Config(r.Context(), scope); err != nil {
		ordersHTTPError(w, err)
		return
	}
	registry, err := a.db.OrderMetadata(r.Context(), scope)
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, registry)
}

func (a *ordersAPI) createMetadata(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		UniqueCode     string `json:"unique_code"`
		Type           string `json:"type"`
		IdempotencyKey string `json:"idempotency_key"`
		Acknowledge    bool   `json:"acknowledge"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	if !body.Acknowledge {
		writeError(w, http.StatusBadRequest, "EXPLICIT_CONFIRMATION_REQUIRED")
		return
	}
	if _, err := a.service.Config(r.Context(), body.scope()); err != nil {
		ordersHTTPError(w, err)
		return
	}
	result, err := a.initializer.EnsureMetadata(r.Context(), body.scope(), body.UniqueCode, body.Type, body.IdempotencyKey, catalogActor(r))
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, result)
}
