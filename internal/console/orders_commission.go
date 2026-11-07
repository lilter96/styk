package console

import (
	"net/http"
	"strconv"

	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
)

func (a *ordersAPI) commissionMetadata(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.CommissionMetadata(r.Context(), orderScope(r))
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, result)
}

func (a *ordersAPI) commissionPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		ExpectedRevision int64                    `json:"expected_revision"`
		Period           orders.RealizationPeriod `json:"period"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	result, err := a.service.PreviewCommission(r.Context(), orderflow.CommissionPreviewRequest{Scope: body.scope(), ExpectedRevision: body.ExpectedRevision, Period: body.Period, Actor: catalogActor(r)})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, result)
}

func (a *ordersAPI) commissionApprove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		PreviewID      string `json:"preview_id"`
		IdempotencyKey string `json:"idempotency_key"`
		Acknowledge    bool   `json:"acknowledge"`
		RetryWorkID    string `json:"retry_work_id,omitempty"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	if !body.Acknowledge {
		writeError(w, http.StatusBadRequest, "EXPLICIT_CONFIRMATION_REQUIRED")
		return
	}
	result, err := a.service.ApproveCommission(r.Context(), orders.CommissionApproval{Scope: body.scope(), PreviewID: body.PreviewID, IdempotencyKey: body.IdempotencyKey, RetryWorkID: body.RetryWorkID, Actor: catalogActor(r)})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, result)
}

func (a *ordersAPI) commissionOperations(w http.ResponseWriter, r *http.Request) {
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			ordersHTTPError(w, orders.ErrInvalid)
			return
		}
		limit = value
	}
	result, err := a.service.CommissionOperations(r.Context(), orderScope(r), limit)
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	if result == nil {
		result = make([]orders.CommissionWork, 0)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, struct {
		Operations []orders.CommissionWork `json:"operations"`
	}{result})
}
