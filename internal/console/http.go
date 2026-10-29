package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"example.com/styk/internal/postgres"
)

// Handler exposes only explicitly scoped reads to an authenticated local operator.
func Handler(db *postgres.Store, secret, origin string) (http.Handler, error) {
	if db == nil {
		return nil, errors.New("console database required")
	}
	a, e := newAuth(secret, origin)
	if e != nil {
		return nil, e
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", a.login)
	mux.HandleFunc("DELETE /api/session", a.authorized(a.logout))
	mux.HandleFunc("GET /api/session", a.authorized(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	mux.HandleFunc("GET /api/registrations", a.authorized(func(w http.ResponseWriter, r *http.Request) {
		tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
		if tenant == "" || len(tenant) > 256 {
			writeError(w, http.StatusBadRequest, "INVALID_SCOPE")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), db.Timeout)
		defer cancel()
		rows, e := db.Pool.Query(ctx, `SELECT source,account_id,registered_at FROM stock_connections WHERE tenant_id=$1 ORDER BY source,account_id LIMIT 101`, tenant)
		if e != nil {
			writeError(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE")
			return
		}
		defer rows.Close()
		type account struct {
			Source       string    `json:"source"`
			Account      string    `json:"account"`
			RegisteredAt time.Time `json:"registered_at"`
		}
		list := []account{}
		for rows.Next() {
			var v account
			if rows.Scan(&v.Source, &v.Account, &v.RegisteredAt) != nil {
				writeError(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE")
				return
			}
			list = append(list, v)
		}
		if rows.Err() != nil {
			writeError(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE")
			return
		}
		truncated := len(list) > 100
		if truncated {
			list = list[:100]
		}
		writeJSON(w, struct {
			Accounts  []account `json:"accounts"`
			Truncated bool      `json:"truncated"`
		}{list, truncated})
	}))
	mux.HandleFunc("GET /api/diagnostics", a.authorized(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		scope := postgres.MatchScope{Tenant: q.Get("tenant"), MoyskladAccount: q.Get("moysklad_account"), OzonAccount: q.Get("ozon_account")}
		if strings.TrimSpace(scope.Tenant) == "" || len(scope.Tenant) > 256 || len(scope.MoyskladAccount) > 256 || len(scope.OzonAccount) > 256 {
			writeError(w, http.StatusBadRequest, "INVALID_SCOPE")
			return
		}
		report, e := db.Doctor(r.Context(), scope, time.Now(), time.Hour)
		if e != nil {
			writeError(w, http.StatusServiceUnavailable, "DIAGNOSTICS_UNAVAILABLE")
			return
		}
		writeJSON(w, report)
	}))
	mux.HandleFunc("GET /api/products", a.authorized(products(db)))
	mux.HandleFunc("GET /api/moysklad-products", a.authorized(searchTargets(db)))
	mux.HandleFunc("GET /api/product-mapping-history", a.authorized(mappingHistory(db)))
	mux.HandleFunc("GET /api/warehouses", a.authorized(warehouses(db)))
	mux.HandleFunc("GET /api/stock", a.authorized(stock(db)))
	return mux, nil
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{code})
}
