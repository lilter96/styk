package console

import (
	"net/http"

	"github.com/lilter96/styk/internal/orders"
)

func (a *ordersAPI) recovery(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.Recovery(r.Context(), orders.RecoveryRequest{Scope: orderScope(r), WorkID: r.URL.Query().Get("work_id"), Actor: catalogActor(r)})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, result)
}

func (a *ordersAPI) recoveryCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		WorkID string `json:"work_id"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	result, err := a.service.CheckRecovery(r.Context(), orders.RecoveryRequest{Scope: body.scope(), WorkID: body.WorkID, Actor: catalogActor(r)})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, result)
}

func (a *ordersAPI) recoveryResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		WorkID         string `json:"work_id"`
		EvidenceID     string `json:"evidence_id"`
		IdempotencyKey string `json:"idempotency_key"`
		Reason         string `json:"reason"`
		Acknowledge    bool   `json:"acknowledge"`
		WorkerStopped  bool   `json:"worker_stopped"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	result, err := a.service.ResolveRecovery(r.Context(), orders.RecoveryResolveRequest{RecoveryRequest: orders.RecoveryRequest{Scope: body.scope(), WorkID: body.WorkID, Actor: catalogActor(r)}, EvidenceID: body.EvidenceID, IdempotencyKey: body.IdempotencyKey, Reason: body.Reason, Acknowledge: body.Acknowledge, WorkerStopped: body.WorkerStopped})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, result)
}
