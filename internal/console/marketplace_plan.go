package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/postgres"
)

type marketplacePlanView struct {
	domain.MarketplaceLimits
	Revision  int64                   `json:"revision"`
	Used      int                     `json:"used"`
	Providers domain.MarketplaceUsage `json:"providers"`
	Exceeded  bool                    `json:"exceeded"`
	Blocked   map[string]string       `json:"blocked"`
}

func planView(state postgres.MarketplacePlanState) marketplacePlanView {
	limits, _ := state.Code.Limits() // Constrained by the workspace schema.
	blocked := make(map[string]string)
	// The other marketplace choices are currently placeholders in the console.
	for _, provider := range []string{"ozon", "wildberries", "yandex"} {
		if reason := marketplaceQuotaCode(limits.Admit(state.Usage, provider)); reason != "" {
			blocked[provider] = reason
		}
	}
	return marketplacePlanView{limits, state.Revision, state.Usage.Total(), state.Usage, limits.Exceeded(state.Usage), blocked}
}

func marketplaceQuotaCode(err error) string {
	switch {
	case errors.Is(err, domain.ErrPlanTotalLimit):
		return "PLAN_TOTAL_LIMIT"
	case errors.Is(err, domain.ErrPlanProviderLimit):
		return "PLAN_PROVIDER_LIMIT"
	case errors.Is(err, domain.ErrPlanOverLimit):
		return "PLAN_OVER_LIMIT"
	default:
		return ""
	}
}

func writeMarketplaceQuotaError(w http.ResponseWriter, err error) bool {
	if code := marketplaceQuotaCode(err); code != "" {
		writeError(w, http.StatusConflict, code)
		return true
	}
	return false
}

func registerMarketplacePlan(mux *http.ServeMux, auth *merchantAuth, db *postgres.Store) {
	if auth == nil {
		return
	}
	mux.HandleFunc("GET /api/marketplace-plan", auth.authorized(func(w http.ResponseWriter, r *http.Request) {
		state, err := db.MarketplacePlan(r.Context(), r.URL.Query().Get("tenant"))
		if err != nil {
			writeMarketplacePlanError(w, err)
			return
		}
		writeJSON(w, planView(state))
	}))
}

func writeMarketplacePlanError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrPlanNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND")
	case errors.Is(err, postgres.ErrPlanRevision):
		writeError(w, http.StatusConflict, "PLAN_REVISION_CONFLICT")
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusForbidden, "FORBIDDEN")
	default:
		writeError(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE")
	}
}

type planCompany struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Email         string                   `json:"email"`
	Code          domain.MarketplacePlan   `json:"code"`
	Revision      int64                    `json:"revision"`
	Providers     domain.MarketplaceUsage  `json:"providers"`
	Used          int                      `json:"used"`
	ExceededPlans []domain.MarketplacePlan `json:"exceeded_plans"`
}

func (a platformAdmin) plans(w http.ResponseWriter, r *http.Request) {
	search, status, limit, offset, ok := adminCompanyPage(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ADMIN_QUERY")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.db.Timeout)
	defer cancel()
	var result struct {
		Total     int                        `json:"total"`
		Companies []planCompany              `json:"companies"`
		Plans     []domain.MarketplaceLimits `json:"plans"`
	}
	var raw []byte
	if err := a.db.Pool.QueryRow(ctx, adminPlansSQL, search, status, limit, offset).Scan(&raw); err != nil || json.Unmarshal(raw, &result) != nil {
		writeError(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE")
		return
	}
	result.Plans = domain.MarketplacePlans()
	for i := range result.Companies {
		company := &result.Companies[i]
		company.Used = company.Providers.Total()
		company.ExceededPlans = make([]domain.MarketplacePlan, 0, len(result.Plans))
		for _, limits := range result.Plans {
			if limits.Exceeded(company.Providers) {
				company.ExceededPlans = append(company.ExceededPlans, limits.Code)
			}
		}
	}
	writeJSON(w, result)
}

func (a platformAdmin) changePlan(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code     domain.MarketplacePlan `json:"code"`
		Revision int64                  `json:"expected_revision"`
	}
	tenant := r.PathValue("tenant")
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if tenant != strings.TrimSpace(tenant) || !validConnectionTenant(tenant) ||
		decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || input.Revision < 1 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	if _, ok := input.Code.Limits(); !ok {
		writeError(w, http.StatusBadRequest, "INVALID_PLAN")
		return
	}
	state, err := a.db.ChangeMarketplacePlan(r.Context(), tenant, input.Code, input.Revision, sessionFrom(r))
	if err != nil {
		writeMarketplacePlanError(w, err)
		return
	}
	writeJSON(w, planView(state))
}

const adminPlansSQL = adminCompaniesSQL + adminCompanyPageSQL + `, usage AS (
 SELECT u.tenant_id,jsonb_object_agg(u.provider,u.used) AS providers
 FROM marketplace_account_usage u JOIN page p USING(tenant_id)
 GROUP BY u.tenant_id
)
SELECT jsonb_build_object(
 'total',(SELECT count(*) FROM filtered),
 'companies',COALESCE(jsonb_agg(jsonb_build_object(
  'id',p.tenant_id,'name',p.name,'email',p.email,
  'code',w.marketplace_plan,'revision',w.marketplace_plan_revision,
  'providers',COALESCE(u.providers,'{}'::jsonb)
 ) ORDER BY lower(p.name),p.tenant_id),'[]'::jsonb)
) FROM page p JOIN saas_workspaces w ON w.id=p.tenant_id LEFT JOIN usage u ON u.tenant_id=p.tenant_id`
