package console

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprovider"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
	"github.com/lilter96/styk/internal/testpublication"
)

type ordersAPI struct {
	db          *postgres.Store
	service     *orderflow.Service
	initializer *orderflow.MetadataInitializer
	factory     orderprovider.Factory
	simulated   bool
}

func registerOrdersRoutes(mux *http.ServeMux, a *auth, db *postgres.Store, c *connections) error {
	gate, err := providercall.New(db, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		return err
	}
	factory := orderprovider.Factory{Credentials: &credentials.Resolver{Store: db, Vault: c.vault}, Gate: gate, DeliveryObserver: db}
	if c.moyFactory != nil {
		factory.NewMoysklad = func(_, secret string) (*moysklad.Client, error) { return c.moyFactory(secret) }
	}
	factory.NewOzon = c.ozonFactory
	api := &ordersAPI{db: db, factory: factory, simulated: c.catalogSimulator}
	api.service, err = orderflow.NewService(db, factory, orderflow.ServiceOptions{AllowScope: api.allowed})
	if err != nil {
		return err
	}
	api.initializer, err = orderflow.NewMetadataInitializer(db, factory, orderflow.ServiceOptions{AllowScope: api.allowed})
	if err != nil {
		return err
	}
	for _, route := range []struct {
		pattern  string
		handler  http.HandlerFunc
		mutation bool
	}{
		{"GET /api/orders/config", api.config, false}, {"POST /api/orders/config", api.saveConfig, true},
		{"GET /api/orders/postings", api.postings, false}, {"GET /api/orders/operations", api.history, false},
		{"GET /api/orders/metadata", api.metadata, false}, {"POST /api/orders/preview", api.preview, true},
		{"GET /api/orders/metadata/registry", api.metadataRegistry, false}, {"POST /api/orders/metadata/attributes", api.createMetadata, true},
		{"POST /api/orders/metadata/reconcile", api.reconcileMetadata, true},
		{"POST /api/orders/approve", api.approve, true}, {"POST /api/orders/check", api.check, true},
	} {
		handler := route.handler
		if route.mutation {
			handler = a.connectionMutation(handler)
		}
		mux.HandleFunc(route.pattern, a.authorized(handler))
	}
	return nil
}
func orderLiveAllowed(scope orders.Scope) bool {
	expected := testpublication.Scope()
	return os.Getenv("STYK_TEST_ACCOUNT_ORDERS") == "true" && scope.Tenant == expected.Tenant && scope.OzonAccount == expected.OzonAccount && scope.MoyskladAccount == expected.MoyskladAccount
}
func (a *ordersAPI) allowed(scope orders.Scope) bool { return a.simulated || orderLiveAllowed(scope) }
func orderScope(r *http.Request) orders.Scope {
	query := r.URL.Query()
	return orders.Scope{Tenant: query.Get("tenant"), OzonAccount: query.Get("ozon_account"), MoyskladAccount: query.Get("moysklad_account")}
}

type orderRequestScope struct {
	Tenant          string `json:"tenant"`
	OzonAccount     string `json:"ozon_account"`
	MoyskladAccount string `json:"moysklad_account"`
}

func (s orderRequestScope) scope() orders.Scope {
	return orders.Scope{Tenant: s.Tenant, OzonAccount: s.OzonAccount, MoyskladAccount: s.MoyskladAccount}
}
func ordersHTTPError(w http.ResponseWriter, err error) {
	status, code := 503, "ORDERS_UNAVAILABLE"
	switch {
	case errors.Is(err, orders.ErrNotFound):
		status, code = 404, "NOT_FOUND"
	case errors.Is(err, orders.ErrInvalid):
		status, code = 400, "INVALID_ORDER_REQUEST"
	case errors.Is(err, orders.ErrConflict), errors.Is(err, orders.ErrStale):
		status, code = 409, "ORDER_EVIDENCE_CHANGED"
	case errors.Is(err, orders.ErrBlocked):
		status, code = 409, "ORDER_REQUIRES_ATTENTION"
	}
	writeError(w, status, code)
}
func (a *ordersAPI) config(w http.ResponseWriter, r *http.Request) {
	config, err := a.service.Config(r.Context(), orderScope(r))
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, struct {
		orders.Config
		Available bool `json:"publication_available"`
	}{config, a.allowed(config.Scope)})
}
func orderPage(r *http.Request) orders.PageRequest {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit == 0 {
		limit = 25
	}
	return orders.PageRequest{Limit: limit, Cursor: query.Get("cursor"), Search: query.Get("search")}
}
func (a *ordersAPI) postings(w http.ResponseWriter, r *http.Request) {
	page, err := a.service.Postings(r.Context(), orderScope(r), orderPage(r))
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, page)
}
func (a *ordersAPI) history(w http.ResponseWriter, r *http.Request) {
	page, err := a.service.History(r.Context(), orderScope(r), orderPage(r))
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	writeJSON(w, page)
}
