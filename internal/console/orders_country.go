package console

import (
	"github.com/lilter96/styk/internal/orders"
	"net/http"
)

func (a *ordersAPI) countries(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	result, err := a.service.Countries(r.Context(), orders.CountriesRequest{Scope: orderScope(r), PostingNumber: query.Get("posting_number"), Search: query.Get("search")})
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, result)
}
