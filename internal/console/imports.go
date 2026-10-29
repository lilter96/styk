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

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

type importAPI struct {
	db                *postgres.Store
	configuredAccount string
	configured        bool
}

var importIdempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func newImportAPI(db *postgres.Store) *importAPI {
	account, key := os.Getenv("OZON_CLIENT_ID"), os.Getenv("OZON_API_KEY")
	return &importAPI{db: db, configuredAccount: account, configured: validConnectionTenant(account) && strings.TrimSpace(key) != "" && !strings.ContainsAny(account+key, "\r\n")}
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
		Type        string               `json:"operation_type"`
		Key         string               `json:"idempotency_key"`
		Acknowledge bool                 `json:"acknowledge"`
		Limits      *domain.ImportLimits `json:"limits,omitempty"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || !validConnectionTenant(in.Tenant) || !validConnectionTenant(in.Account) || in.Type != domain.OzonCatalogImport || !in.Acknowledge || !importIdempotencyPattern.MatchString(in.Key) {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	// Only the locally configured account is eligible; an external ID from the
	// browser is not ownership evidence and never supplies marketplace credentials.
	if !a.configured || in.Account != a.configuredAccount {
		writeError(w, 403, "ACCOUNT_NOT_CONFIGURED")
		return
	}
	limits := domain.DefaultOzonImportLimits()
	if in.Limits != nil {
		limits = *in.Limits
	}
	if !limits.Valid() {
		writeError(w, 400, "INVALID_LIMITS")
		return
	}
	session, _ := r.Context().Value(auditContextKey{}).(string)
	if session == "" {
		writeError(w, 401, "UNAUTHORIZED")
		return
	}
	op, replay, err := a.db.SubmitOzonImport(r.Context(), in.Tenant, in.Account, in.Key, session, limits)
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
func importQuery(r *http.Request) (string, string, bool) {
	tenant, account := r.URL.Query().Get("tenant"), r.URL.Query().Get("ozon_account")
	return tenant, account, validConnectionTenant(tenant) && validConnectionTenant(account)
}
func (a *importAPI) get(w http.ResponseWriter, r *http.Request) {
	tenant, account, ok := importQuery(r)
	if !ok {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	op, err := a.db.GetImportOperation(r.Context(), tenant, account, r.PathValue("id"))
	if err != nil {
		importError(w, err)
		return
	}
	writeJSON(w, op)
}
func (a *importAPI) list(w http.ResponseWriter, r *http.Request) {
	tenant, account, ok := importQuery(r)
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
	rows, more, err := a.db.ListImportOperations(r.Context(), tenant, account, limit, offset)
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
		Acknowledge bool   `json:"acknowledge"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || !validConnectionTenant(in.Tenant) || !validConnectionTenant(in.Account) || !in.Acknowledge {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	if err := a.db.CancelQueuedImport(r.Context(), in.Tenant, in.Account, r.PathValue("id")); err != nil {
		importError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
