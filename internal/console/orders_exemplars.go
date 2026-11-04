package console

import "net/http"

func (a *ordersAPI) exemplarSnapshot(w http.ResponseWriter, r *http.Request) {
	result, err := a.db.OrderExemplarPublic(r.Context(), orderScope(r), r.URL.Query().Get("posting_number"))
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, result)
}
