package console

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogprovider"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
	"github.com/lilter96/styk/internal/testpublication"
	"time"
)

type catalogAPI struct {
	db        *postgres.Store
	service   catalogsync.Service
	factory   catalogprovider.Factory
	simulated bool
}

func registerCatalogRoutes(mux *http.ServeMux, a *auth, db *postgres.Store, c *connections) {
	factory := catalogFactory(c)
	api := &catalogAPI{db: db, factory: factory, simulated: c.catalogSimulator}
	api.service = catalogsync.Service{Store: db, Resolver: factory, AllowScope: api.allowed}
	mux.HandleFunc("GET /api/catalog-sync/config", a.authorized(api.audit(api.config)))
	mux.HandleFunc("POST /api/catalog-sync/config", a.authorized(a.connectionMutation(api.audit(api.saveConfig))))
	mux.HandleFunc("GET /api/catalog-sync/products", a.authorized(api.audit(api.products)))
	mux.HandleFunc("POST /api/catalog-sync/preview", a.authorized(a.connectionMutation(api.audit(api.preview))))
	mux.HandleFunc("POST /api/catalog-sync/reconcile", a.authorized(a.connectionMutation(api.audit(api.reconcile))))
	mux.HandleFunc("POST /api/catalog-sync/approve", a.authorized(a.connectionMutation(api.audit(api.approve))))
	mux.HandleFunc("GET /api/catalog-sync/operations", a.authorized(api.audit(api.operations)))
	mux.HandleFunc("GET /api/catalog-sync/categories", a.authorized(api.audit(api.categories)))
	mux.HandleFunc("GET /api/catalog-sync/attributes", a.authorized(api.audit(api.attributes)))
}

func catalogFactory(c *connections) catalogprovider.Factory {
	gate, err := providercall.New(c.db, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		panic("invalid catalog provider coordinator")
	}
	factory := catalogprovider.Factory{Credentials: &credentials.Resolver{Store: c.db, Vault: c.vault}, Gate: gate}
	if c.moyFactory != nil {
		factory.NewMoysklad = func(_, secret string) (*moysklad.Client, error) { return c.moyFactory(secret) }
	}
	if c.ozonFactory != nil {
		factory.NewOzon = c.ozonFactory
	}
	return factory
}

func catalogLiveAllowed(scope catalog.Scope) bool {
	expected := testpublication.Scope()
	return os.Getenv("STYK_TEST_ACCOUNT_CATALOG") == "true" && scope.Tenant == expected.Tenant && scope.MoyskladAccount == expected.MoyskladAccount && scope.OzonAccount == expected.OzonAccount
}
func (a *catalogAPI) allowed(scope catalog.Scope) bool {
	return a.simulated || catalogLiveAllowed(scope)
}
func catalogScope(r *http.Request) catalog.Scope {
	q := r.URL.Query()
	return catalog.Scope{Tenant: q.Get("tenant"), MoyskladAccount: q.Get("moysklad_account"), OzonAccount: q.Get("ozon_account")}
}
func catalogActor(r *http.Request) string {
	actor, _ := r.Context().Value(auditContextKey{}).(string)
	return actor
}

type catalogRequestScope struct {
	Tenant          string `json:"tenant"`
	MoyskladAccount string `json:"moysklad_account"`
	OzonAccount     string `json:"ozon_account"`
}

func (s catalogRequestScope) catalogScope() catalog.Scope {
	return catalog.Scope{Tenant: s.Tenant, MoyskladAccount: s.MoyskladAccount, OzonAccount: s.OzonAccount}
}

func (a *catalogAPI) config(w http.ResponseWriter, r *http.Request) {
	config, err := a.service.Config(r.Context(), catalogScope(r))
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	writeJSON(w, struct {
		catalogsync.Config
		Available bool                     `json:"publication_available"`
		Fields    []catalogFieldCapability `json:"fields"`
	}{config, a.allowed(config.Scope), catalogCapabilities()})
}

type catalogFieldCapability struct {
	Field    catalog.Field `json:"field"`
	Writable bool          `json:"writable"`
	Reason   string        `json:"reason,omitempty"`
}

func catalogCapabilities() []catalogFieldCapability {
	fields := []catalog.Field{catalog.FieldName, catalog.FieldDescription, catalog.FieldBarcodes, catalog.FieldArticle, catalog.FieldPrice, catalog.FieldCurrency, catalog.FieldCategory, catalog.FieldAttributes, catalog.FieldImages, catalog.FieldUnit, catalog.FieldDimensions}
	result := make([]catalogFieldCapability, 0, len(fields))
	for _, field := range fields {
		reason := ""
		if !field.LiveWritable() {
			reason = "Для изменения этого поля требуется поддерживаемый контракт и фактические данные товара."
		}
		result = append(result, catalogFieldCapability{field, field.LiveWritable(), reason})
	}
	return result
}

func (a *catalogAPI) saveConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		catalogRequestScope
		Config           catalogsync.Config `json:"config"`
		ExpectedRevision int64              `json:"expected_revision"`
		IdempotencyKey   string             `json:"idempotency_key"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	scope := body.catalogScope()
	if body.Config.Scope.Tenant != "" && body.Config.Scope.Tenant != scope.Tenant {
		writeError(w, 404, "NOT_FOUND")
		return
	}
	if scope.MoyskladAccount == "" {
		scope.MoyskladAccount = body.Config.Scope.MoyskladAccount
	}
	if scope.OzonAccount == "" {
		scope.OzonAccount = body.Config.Scope.OzonAccount
	}
	if body.Config.Scope.MoyskladAccount != "" && body.Config.Scope.MoyskladAccount != scope.MoyskladAccount || body.Config.Scope.OzonAccount != "" && body.Config.Scope.OzonAccount != scope.OzonAccount {
		writeError(w, 400, "CATALOG_SCOPE_CHANGED")
		return
	}
	body.Config.Scope = scope
	config, err := a.service.SaveConfig(r.Context(), catalogsync.SaveConfigRequest{Config: body.Config, ExpectedRevision: body.ExpectedRevision, Actor: catalogActor(r), IdempotencyKey: body.IdempotencyKey})
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	writeJSON(w, config)
}

func (a *catalogAPI) preview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		catalogRequestScope
		ExpectedRevision int64                                      `json:"expected_revision"`
		EnrollmentIDs    []string                                   `json:"enrollment_ids"`
		ManualValues     map[string]map[catalog.Field]catalog.Value `json:"manual_values"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	preview, err := a.service.Preview(r.Context(), catalogsync.PreviewRequest{Scope: body.catalogScope(), ExpectedRevision: body.ExpectedRevision, EnrollmentIDs: body.EnrollmentIDs, ManualValues: body.ManualValues, Actor: catalogActor(r)})
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	writeJSON(w, preview)
}

func (a *catalogAPI) approve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		catalogRequestScope
		PreviewID      string   `json:"preview_id"`
		EnrollmentIDs  []string `json:"enrollment_ids"`
		Acknowledge    bool     `json:"acknowledge"`
		IdempotencyKey string   `json:"idempotency_key"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	if !body.Acknowledge {
		writeError(w, 400, "CATALOG_ACKNOWLEDGMENT_REQUIRED")
		return
	}
	operation, err := a.service.Approve(r.Context(), catalogsync.Approval{Scope: body.catalogScope(), PreviewID: body.PreviewID, EnrollmentIDs: body.EnrollmentIDs, Actor: catalogActor(r), IdempotencyKey: body.IdempotencyKey})
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	writeJSON(w, operation)
}

func catalogPage(r *http.Request) catalogsync.PageRequest {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return catalogsync.PageRequest{Limit: limit, Cursor: r.URL.Query().Get("cursor"), Search: r.URL.Query().Get("search"), Provider: catalog.Provider(r.URL.Query().Get("provider"))}
}
func (a *catalogAPI) products(w http.ResponseWriter, r *http.Request) {
	scope := catalogScope(r)
	if _, err := a.service.Config(r.Context(), scope); err != nil {
		catalogHTTPError(w, err)
		return
	}
	page, err := a.service.Products(r.Context(), scope, catalogPage(r))
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	enrollments, err := a.enrollments(r.Context(), scope, page.Products)
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	writeJSON(w, struct {
		catalogsync.ProductPage
		Enrollments []catalogsync.Enrollment `json:"enrollments"`
	}{page, enrollments})
}
func (a *catalogAPI) operations(w http.ResponseWriter, r *http.Request) {
	scope := catalogScope(r)
	if _, err := a.service.Config(r.Context(), scope); err != nil {
		catalogHTTPError(w, err)
		return
	}
	page, err := a.service.History(r.Context(), scope, catalogPage(r))
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	writeJSON(w, page)
}

func (a *catalogAPI) enrollments(ctx context.Context, scope catalog.Scope, products []catalog.Product) ([]catalogsync.Enrollment, error) {
	ids := make([]string, 0, len(products))
	provider := catalog.ProviderMoySklad
	for _, p := range products {
		ids = append(ids, p.Identity.ID)
		provider = p.Identity.Account.Provider
	}
	ctx, cancel := context.WithTimeout(ctx, a.db.Timeout)
	defer cancel()
	rows, err := a.db.Pool.Query(ctx, `SELECT id,version,target_kind,target_id,ozon_product_id,ozon_offer_id FROM product_mappings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND (($5='moysklad' AND target_id=ANY($4)) OR ($5='ozon' AND ozon_product_id=ANY($4))) ORDER BY id LIMIT 101`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, ids, string(provider))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]catalogsync.Enrollment, 0, len(products))
	for rows.Next() {
		var id, version int64
		var kind, source, target, offer string
		if err := rows.Scan(&id, &version, &kind, &source, &target, &offer); err != nil {
			return nil, err
		}
		result = append(result, catalogsync.Enrollment{ID: "mapping-" + strconv.FormatInt(id, 10), MappingID: strconv.FormatInt(id, 10), MappingVersion: version, Source: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderMoySklad, ID: scope.MoyskladAccount}, EntityType: kind, ID: source}, Target: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon, ID: scope.OzonAccount}, EntityType: "product", ID: target, OfferID: offer}})
	}
	return result, rows.Err()
}

func catalogHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalogsync.ErrNotFound), errors.Is(err, credentials.ErrOwnership):
		writeError(w, 404, "CATALOG_NOT_FOUND")
	case errors.Is(err, catalogsync.ErrStale):
		writeError(w, 409, "CATALOG_REVIEW_STALE")
	case errors.Is(err, catalogsync.ErrConflict):
		writeError(w, 409, "CATALOG_CONFLICT")
	case errors.Is(err, catalogsync.ErrBlocked):
		writeError(w, 409, "CATALOG_PUBLICATION_BLOCKED")
	case errors.Is(err, catalogsync.ErrInvalid), errors.Is(err, catalog.ErrInvalidScope), errors.Is(err, catalog.ErrInvalidPolicy), errors.Is(err, catalog.ErrInvalidIdentity), errors.Is(err, catalog.ErrInvalidCommand):
		writeError(w, 400, "CATALOG_INVALID_REQUEST")
	default:
		writeError(w, 503, "CATALOG_SERVICE_UNAVAILABLE")
	}
}

func (a *catalogAPI) audit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r.WithContext(catalogprovider.AuditRequests(r.Context())))
	}
}

func (a *catalogAPI) reconcile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		catalogRequestScope
		WorkID         string `json:"work_id"`
		IdempotencyKey string `json:"idempotency_key"`
		Acknowledge    bool   `json:"acknowledge"`
		AcceptWarnings bool   `json:"accept_warnings"`
		Reason         string `json:"reason"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	scope := body.catalogScope()
	if _, err := a.service.Config(r.Context(), scope); err != nil {
		catalogHTTPError(w, err)
		return
	}
	runner := catalogsync.Reconciler{Store: a.db, Resolver: a.factory, AllowScope: a.allowed}
	result, err := runner.Reconcile(r.Context(), catalogsync.ReconciliationRequest{Scope: scope, WorkID: body.WorkID, Actor: catalogActor(r), IdempotencyKey: body.IdempotencyKey, Acknowledge: body.Acknowledge, AcceptWarnings: body.AcceptWarnings, Reason: body.Reason})
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	writeJSON(w, result)
}
