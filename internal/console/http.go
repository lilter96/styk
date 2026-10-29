package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/postgres"
)

// Handler exposes scoped reads and explicit local mapping operations to an authenticated local operator.
func Handler(db *postgres.Store, secret, origin string) (http.Handler, error) {
	return handlerWithConnections(db, secret, origin, nil)
}

// Injection is used by package tests; the production handler always constructs
// clients for the official provider hosts from local process configuration.
func handlerWithConnections(db *postgres.Store, secret, origin string, connections *connections) (http.Handler, error) {
	if db == nil {
		return nil, errors.New("console database required")
	}
	a, e := newAuth(secret, origin)
	if e != nil {
		return nil, e
	}
	lifetime := 5 * time.Minute
	if raw := os.Getenv("STYK_REVIEW_LIFETIME"); raw != "" {
		var err error
		lifetime, err = time.ParseDuration(raw)
		if err != nil || lifetime <= 0 || lifetime > 10*time.Minute {
			return nil, errors.New("STYK_REVIEW_LIFETIME must be positive and at most 10 minutes")
		}
	}
	mux := http.NewServeMux()
	if connections == nil {
		connections = newConnections(db)
	}
	mux.HandleFunc("POST /api/session", a.login)
	mux.HandleFunc("DELETE /api/session", a.authorized(a.logout))
	mux.HandleFunc("GET /api/session", a.authorized(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	mux.HandleFunc("GET /api/integrations", a.authorized(connections.list))
	mux.HandleFunc("POST /api/integrations/{provider}/probe", a.authorized(a.connectionMutation(connections.probe)))
	mux.HandleFunc("POST /api/integrations/{provider}/register", a.authorized(a.connectionMutation(connections.register)))
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
	mux.HandleFunc("POST /api/product-mapping-reviews", a.authorized(a.createMappingReview(db, lifetime)))
	mux.HandleFunc("POST /api/product-mapping-reviews/{id}/confirm", a.authorized(a.confirmMappingReview(db)))
	mux.HandleFunc("GET /api/product-mapping-reviews/{id}", a.authorized(a.inspectMappingReview(db)))
	mux.HandleFunc("GET /api/products", a.authorized(products(db)))
	mux.HandleFunc("GET /api/moysklad-products", a.authorized(searchTargets(db)))
	mux.HandleFunc("GET /api/product-mapping-history", a.authorized(mappingHistory(db)))
	mux.HandleFunc("POST /api/warehouse-mapping-reviews", a.authorized(a.createWarehouseReview(db, lifetime)))
	mux.HandleFunc("POST /api/warehouse-mapping-reviews/{id}/confirm", a.authorized(a.confirmWarehouseReview(db)))
	mux.HandleFunc("GET /api/warehouse-mapping-reviews/{id}", a.authorized(a.inspectWarehouseReview(db)))
	mux.HandleFunc("GET /api/warehouse-mapping-review", a.authorized(warehouseReview(db)))
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
