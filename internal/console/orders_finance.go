package console

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/lilter96/styk/internal/orders"
)

func (a *ordersAPI) realizationReport(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	year, yearErr := strconv.Atoi(query.Get("year"))
	month, monthErr := strconv.Atoi(query.Get("month"))
	if yearErr != nil || monthErr != nil || len(query["year"]) != 1 || len(query["month"]) != 1 {
		ordersHTTPError(w, orders.ErrInvalid)
		return
	}
	report, err := a.service.RealizationReport(r.Context(), orders.RealizationRequest{Scope: orderScope(r), Period: orders.RealizationPeriod{Year: year, Month: month}})
	if err != nil {
		var unavailable *orders.RealizationReadError
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
