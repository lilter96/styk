package console

import (
	"net/http"
	"strconv"

	"github.com/lilter96/styk/internal/orders"
)

func (a *ordersAPI) merchantIntents(w http.ResponseWriter, r *http.Request) {
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			ordersHTTPError(w, orders.ErrInvalid)
			return
		}
		limit = parsed
	}
	items, err := a.service.MerchantIntents(r.Context(), orderScope(r), limit)
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, struct {
		Intents []orders.MerchantIntentStatus `json:"intents"`
	}{items})
}
