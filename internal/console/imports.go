package console

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

type importAPI struct {
	db                *postgres.Store
	configuredAccount string
	configured        bool
	moyAccount        string
	moyConfigured     bool
	vault             *credentials.Vault
}

var importIdempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func newImportAPI(db *postgres.Store) *importAPI {
	account, key := os.Getenv("OZON_CLIENT_ID"), os.Getenv("OZON_API_KEY")
	moyAccount, token := os.Getenv("MOYSKLAD_ACCOUNT_ID"), os.Getenv("MOYSKLAD_TOKEN")
	vault, _ := credentials.FromEnvironment()
	return &importAPI{db: db, vault: vault, moyAccount: moyAccount, moyConfigured: validConnectionTenant(moyAccount) && strings.TrimSpace(token) != "" && !strings.ContainsAny(moyAccount+token, "\r\n"), configuredAccount: account, configured: validConnectionTenant(account) && strings.TrimSpace(key) != "" && !strings.ContainsAny(account+key, "\r\n")}
}
func importError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrImportScope), errors.Is(err, postgres.ErrImportNotFound):
		writeError(w, 404, "IMPORT_NOT_FOUND")
	case errors.Is(err, postgres.ErrImportIdempotency):
		writeError(w, 409, "IDEMPOTENCY_CONFLICT")
	case errors.Is(err, postgres.ErrImportConflict):
		writeError(w, 409, "IMPORT_ALREADY_ACTIVE")
	case errors.Is(err, postgres.ErrImportRunning):
		writeError(w, 409, "CANCELLATION_UNAVAILABLE")
	default:
		writeError(w, 503, "IMPORT_PERSISTENCE_UNKNOWN")
	}
}
func (a *importAPI) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tenant      string               `json:"tenant"`
		Account     string               `json:"ozon_account"`
		MoyAccount  string               `json:"moysklad_account"`
		Type        string               `json:"operation_type"`
		Key         string               `json:"idempotency_key"`
		Acknowledge bool                 `json:"acknowledge"`
		Limits      *domain.ImportLimits `json:"limits,omitempty"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	decodeErr := d.Decode(&in)
	provider := "ozon"
	if in.Type == domain.MoyskladCatalogImport {
		provider = "moysklad"
	}
	invalidPair := in.Account != "" && in.MoyAccount != "" || provider == "ozon" && in.MoyAccount != "" || provider == "moysklad" && in.Account != ""
	if provider == "moysklad" {
		in.Account = in.MoyAccount
	}
	if decodeErr != nil || d.Decode(&struct{}{}) != io.EOF || invalidPair || !validConnectionTenant(in.Tenant) || !validConnectionTenant(in.Account) || (in.Type != domain.OzonCatalogImport && in.Type != domain.MoyskladCatalogImport) || !in.Acknowledge || !importIdempotencyPattern.MatchString(in.Key) {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	limits := domain.DefaultOzonImportLimits()
	if provider == "moysklad" {
		limits = domain.DefaultMoyskladImportLimits()
	}
	if in.Limits != nil {
		limits = *in.Limits
	}
	if !limits.ValidFor(provider) {
		writeError(w, 400, "INVALID_LIMITS")
		return
	}
	session, _ := r.Context().Value(auditContextKey{}).(string)
	if session == "" {
		writeError(w, 401, "UNAUTHORIZED")
		return
	}
	// An accepted intent remains recoverable after credential rotation/disconnect;
	// only genuinely new submissions require an available scoped credential.
	exists, err := a.db.CatalogImportExists(r.Context(), in.Tenant, provider, in.Account, in.Key)
	if err != nil {
		importError(w, err)
		return
	}
	if !exists {
		configured := provider == "ozon" && a.configured && in.Account == a.configuredAccount || provider == "moysklad" && a.moyConfigured && in.Account == a.moyAccount
		if a.vault != nil || configured {
			_, resolveErr := (credentials.Resolver{Store: a.db, Vault: a.vault}).Resolve(r.Context(), credentials.Scope{Tenant: in.Tenant, Provider: provider, Account: in.Account})
			if errors.Is(resolveErr, credentials.ErrOwnership) {
				importError(w, postgres.ErrImportScope)
				return
			}
			configured = resolveErr == nil
		}
		if !configured {
			writeError(w, 403, "ACCOUNT_NOT_CONFIGURED")
			return
		}
	}

	op, replay, err := a.db.SubmitCatalogImport(r.Context(), in.Tenant, provider, in.Account, in.Key, session, limits)
	if err != nil {
		importError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if !replay {
		w.WriteHeader(http.StatusCreated)
	}
	writeJSON(w, struct {
		Operation        domain.ImportOperation `json:"operation"`
		IdempotentReplay bool                   `json:"idempotent_replay"`
	}{op, replay})
}
func importQuery(r *http.Request) (string, string, string, bool) {
	tenant, account := r.URL.Query().Get("tenant"), r.URL.Query().Get("ozon_account")
	provider := "ozon"
	moy := r.URL.Query().Get("moysklad_account")
	if moy != "" && account != "" {
		return "", "", "", false
	}
	if moy != "" {
		provider, account = "moysklad", moy
	}
	return tenant, provider, account, validConnectionTenant(tenant) && validConnectionTenant(account)
}
func (a *importAPI) get(w http.ResponseWriter, r *http.Request) {
	tenant, provider, account, ok := importQuery(r)
	if !ok {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	op, err := a.db.GetCatalogOperation(r.Context(), tenant, provider, account, r.PathValue("id"))
	if err != nil {
		importError(w, err)
		return
	}
	writeJSON(w, op)
}
func (a *importAPI) list(w http.ResponseWriter, r *http.Request) {
	tenant, provider, account, ok := importQuery(r)
	if !ok {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	limit, offset := 20, 0
	var err error
	if s := r.URL.Query().Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
	}
	if s := r.URL.Query().Get("offset"); s != "" {
		offset, err = strconv.Atoi(s)
		if err != nil {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
	}
	if limit < 1 || limit > 50 || offset < 0 || offset > 10000 {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	rows, more, err := a.db.ListCatalogOperations(r.Context(), tenant, provider, account, limit, offset)
	if err != nil {
		importError(w, err)
		return
	}
	writeJSON(w, struct {
		Items   []domain.ImportOperation `json:"items"`
		HasNext bool                     `json:"has_next"`
		Offset  int                      `json:"offset"`
	}{rows, more, offset})
}
func (a *importAPI) cancel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tenant      string `json:"tenant"`
		Account     string `json:"ozon_account"`
		MoyAccount  string `json:"moysklad_account"`
		Acknowledge bool   `json:"acknowledge"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	decodeErr := d.Decode(&in)
	provider := "ozon"
	invalidPair := in.Account != "" && in.MoyAccount != ""
	if in.MoyAccount != "" {
		provider, in.Account = "moysklad", in.MoyAccount
	}
	if invalidPair || decodeErr != nil || d.Decode(&struct{}{}) != io.EOF || !validConnectionTenant(in.Tenant) || !validConnectionTenant(in.Account) || !in.Acknowledge {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	if err := a.db.CancelCatalogImport(r.Context(), in.Tenant, provider, in.Account, r.PathValue("id")); err != nil {
		importError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
