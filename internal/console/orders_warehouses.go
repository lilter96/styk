package console

import "net/http"

func (a *ordersAPI) warehouses(w http.ResponseWriter, r *http.Request) {
	scope := orderScope(r)
	if _, err := a.service.Config(r.Context(), scope); err != nil {
		ordersHTTPError(w, err)
		return
	}
	page, err := a.db.OrderWarehouses(r.Context(), scope, orderPage(r))
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, page)
}
