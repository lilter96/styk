package console

import (
	"net/http"
	"time"

	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/orders"
)

func (a *ordersAPI) saveConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		Config           orders.Config `json:"config"`
		ExpectedRevision int64         `json:"expected_revision"`
		IdempotencyKey   string        `json:"idempotency_key"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	scope := body.scope()
	if body.Config.Scope != (orders.Scope{}) && body.Config.Scope != scope {
		writeError(w, 404, "NOT_FOUND")
		return
	}
	body.Config.Scope = scope
	config, err := a.service.SaveConfig(r.Context(), orders.SaveConfigRequest{Config: body.Config, ExpectedRevision: body.ExpectedRevision, IdempotencyKey: body.IdempotencyKey, Actor: catalogActor(r)})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, config)
}
func (a *ordersAPI) preview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		ExpectedRevision int64                   `json:"expected_revision"`
		PostingNumbers   []string                `json:"posting_numbers"`
		Actions          []orders.Action         `json:"actions"`
		Proposals        []orders.ActionProposal `json:"proposals"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	result, err := a.service.Preview(r.Context(), orders.PreviewRequest{Scope: body.scope(), ExpectedRevision: body.ExpectedRevision, PostingNumbers: body.PostingNumbers, Actions: body.Actions, Proposals: body.Proposals, Actor: catalogActor(r)})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, result)
}
func (a *ordersAPI) approve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		PreviewID      string   `json:"preview_id"`
		PostingNumbers []string `json:"posting_numbers"`
		IdempotencyKey string   `json:"idempotency_key"`
		Acknowledge    bool     `json:"acknowledge"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	if !body.Acknowledge {
		writeError(w, 400, "EXPLICIT_CONFIRMATION_REQUIRED")
		return
	}
	result, err := a.service.Approve(r.Context(), orders.Approval{Scope: body.scope(), PreviewID: body.PreviewID, PostingNumbers: body.PostingNumbers, IdempotencyKey: body.IdempotencyKey, Actor: catalogActor(r)})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, result)
}
func (a *ordersAPI) metadata(w http.ResponseWriter, r *http.Request) {
	scope := orderScope(r)
	if _, err := a.service.Config(r.Context(), scope); err != nil {
		ordersHTTPError(w, err)
		return
	}
	binding, err := a.factory.ResolveOrder(r.Context(), scope, "moysklad", scope.MoyskladAccount, 0)
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	client, ok := binding.Client.(*moysklad.OrderClient)
	if !ok {
		ordersHTTPError(w, orders.ErrBlocked)
		return
	}
	result, err := client.ReadOrderMetadata(r.Context(), scope)
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, result)
}
func (a *ordersAPI) check(w http.ResponseWriter, r *http.Request) {
	var body struct{ orderRequestScope }
	if !decodeStockSync(w, r, &body) {
		return
	}
	if err := a.db.RequestOrderCheck(r.Context(), body.scope(), catalogActor(r)); err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, struct {
		Scheduled   bool      `json:"scheduled"`
		RequestedAt time.Time `json:"requested_at"`
	}{true, time.Now().UTC()})
}
