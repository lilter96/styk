package console

import (
	"net/http"
	"strconv"

	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
)

func (a *ordersAPI) carriagePreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		ExpectedRevision int64 `json:"expected_revision"`
		orders.CarriageInput
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	result, err := a.service.PreviewCarriage(r.Context(), orderflow.CarriagePreviewRequest{Scope: body.scope(), ExpectedRevision: body.ExpectedRevision, Actor: catalogActor(r), Input: body.CarriageInput})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, result)
}

func (a *ordersAPI) carriageApprove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		PreviewID                  string `json:"preview_id"`
		IdempotencyKey             string `json:"idempotency_key"`
		Acknowledge                bool   `json:"acknowledge"`
		AllReadyConsent            bool   `json:"all_ready_consent"`
		PhysicalReadinessConfirmed bool   `json:"physical_readiness_confirmed"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	if !body.Acknowledge {
		writeError(w, http.StatusBadRequest, "EXPLICIT_CONFIRMATION_REQUIRED")
		return
	}
	result, err := a.service.ApproveCarriage(r.Context(), orders.CarriageApproval{Scope: body.scope(), PreviewID: body.PreviewID, IdempotencyKey: body.IdempotencyKey, AllReadyConsent: body.AllReadyConsent, PhysicalReadinessConfirmed: body.PhysicalReadinessConfirmed, Actor: catalogActor(r)})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, result)
}

func (a *ordersAPI) carriageOperations(w http.ResponseWriter, r *http.Request) {
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			ordersHTTPError(w, orders.ErrInvalid)
			return
		}
		limit = parsed
	}
	result, err := a.service.CarriageOperations(r.Context(), orderScope(r), limit)
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	if result == nil {
		result = make([]orders.CarriageWork, 0)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, struct {
		Operations []orders.CarriageWork `json:"operations"`
	}{result})
}

func (a *ordersAPI) carriageReread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		WorkID string `json:"work_id"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	if err := a.service.RereadCarriage(r.Context(), body.scope(), body.WorkID, catalogActor(r)); err != nil {
		ordersHTTPError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, struct {
		Queued bool `json:"queued"`
	}{true})
}
