package console

import (
	"errors"
	"net/http"

	"github.com/lilter96/styk/internal/orders"
)

func (a *ordersAPI) accrualTypes(w http.ResponseWriter, r *http.Request) {
	a.writeAccrualReport(w, r, orderScope(r), orders.AccrualRequest{Kind: orders.AccrualTypes})
}

func (a *ordersAPI) accrualByDay(w http.ResponseWriter, r *http.Request) {
	a.writeAccrualReport(w, r, orderScope(r), orders.AccrualRequest{Kind: orders.AccrualByDay, Date: r.URL.Query().Get("date")})
}

func (a *ordersAPI) accrualPostings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orderRequestScope
		PostingNumbers []string `json:"posting_numbers"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	a.writeAccrualReport(w, r, body.scope(), orders.AccrualRequest{Kind: orders.AccrualPostings, PostingNumbers: body.PostingNumbers})
}

func (a *ordersAPI) writeAccrualReport(w http.ResponseWriter, r *http.Request, scope orders.Scope, request orders.AccrualRequest) {
	report, err := a.service.AccrualReport(r.Context(), scope, request, catalogActor(r))
	if err != nil {
		var unavailable *orders.AccrualReadError
		if errors.As(err, &unavailable) {
			writeError(w, http.StatusServiceUnavailable, unavailable.Code)
			return
		}
		ordersHTTPError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, report)
}
