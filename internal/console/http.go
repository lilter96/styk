package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/identity"
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
	return handlerWithAuth(db, a, connections)
}

// MerchantHandler exposes the existing business routes behind persistent
// identity, workspace membership and role checks. It requires private delivery
// of email verification and password recovery links.
func MerchantHandler(db *postgres.Store, origin string, sender identity.Sender) (http.Handler, error) {
	if db == nil {
		return nil, errors.New("console database required")
	}
	a, err := newMerchantAuth(db, origin, sender)
	if err != nil {
		return nil, err
	}
	return handlerWithAuth(db, a, nil)
}

func handlerWithAuth(db *postgres.Store, a *auth, connections *connections) (http.Handler, error) {
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
		if _, err := credentials.FromEnvironment(); err != nil {
			return nil, err
		}
		connections = newConnections(db)
	}
	registerCredentialRoutes(mux, a, db, connections)
	registerStockSyncRoutes(mux, a, db, connections)
	registerOrderEvidenceRoutes(mux, a, db, connections)
	registerCatalogRoutes(mux, a, db, connections)
	if err := registerOrdersRoutes(mux, a, db, connections); err != nil {
		return nil, err
	}
	imports := newImportAPI(db)
	mux.HandleFunc("GET /api/auth-mode", func(w http.ResponseWriter, r *http.Request) {
		valid := a.merchant == nil && a.safe(r) || a.merchant != nil && a.merchant.safe(r)
		if !valid {
			writeError(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		mode := "local"
		if a.merchant != nil {
			mode = "merchant"
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, struct {
			Mode string `json:"mode"`
		}{mode})
	})
	if a.merchant == nil {
		mux.HandleFunc("POST /api/session", a.login)
		mux.HandleFunc("DELETE /api/session", a.authorized(a.logout))
		mux.HandleFunc("GET /api/session", a.authorized(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	} else {
		registerMerchantRoutes(mux, a)
	}
	mux.HandleFunc("POST /api/imports", a.authorized(a.connectionMutation(imports.create)))
	mux.HandleFunc("GET /api/imports", a.authorized(imports.list))
	mux.HandleFunc("GET /api/imports/{id}", a.authorized(imports.get))
	mux.HandleFunc("POST /api/imports/{id}/cancel", a.authorized(a.connectionMutation(imports.cancel)))
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
		query := `SELECT source,account_id,registered_at FROM stock_connections WHERE tenant_id=$1 ORDER BY source,account_id LIMIT 101`
		if connections.vault != nil {
			query = `SELECT c.source,c.account_id,c.registered_at,COALESCE(k.name,'') FROM stock_connections c LEFT JOIN connection_credentials k ON k.tenant_id=c.tenant_id AND k.provider=c.source AND k.account_id=c.account_id WHERE c.tenant_id=$1 ORDER BY c.source,c.account_id LIMIT 101`
		}
		rows, e := db.Pool.Query(ctx, query, tenant)
		if e != nil {
			writeError(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE")
			return
		}
		defer rows.Close()
		type account struct {
			Source       string    `json:"source"`
			Account      string    `json:"account"`
			RegisteredAt time.Time `json:"registered_at"`
			Name         string    `json:"name,omitempty"`
		}
		list := []account{}
		for rows.Next() {
			var v account
			var scanErr error
			if connections.vault != nil {
				scanErr = rows.Scan(&v.Source, &v.Account, &v.RegisteredAt, &v.Name)
			} else {
				scanErr = rows.Scan(&v.Source, &v.Account, &v.RegisteredAt)
			}
			if scanErr != nil {
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
