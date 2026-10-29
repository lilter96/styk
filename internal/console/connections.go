package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/doctor"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
)

// These clients are shared so concurrent operator probes use their existing rate limiter.
// Only the hard-coded official hosts are used in the production constructor.
type connections struct {
	catalogSimulator        bool // Only set by the integration-only literal-loopback harness.
	db                      *postgres.Store
	moyAccount, ozonAccount string
	moy                     *moysklad.Client
	ozon                    *ozon.Client
	mu                      sync.Mutex
	checks                  map[string]map[string]connectionCheck
	vault                   *credentials.Vault
	moyFactory              func(string) (*moysklad.Client, error)
	ozonFactory             func(string, string) (*ozon.Client, error)
}
type connectionCheck struct {
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at"`
	Identity  string    `json:"identity"`
	Reason    string    `json:"reason,omitempty"`
}
type providerView struct {
	Provider           string           `json:"provider"`
	Account            string           `json:"account,omitempty"`
	Status             string           `json:"status"`
	Check              *connectionCheck `json:"check,omitempty"`
	Registration       string           `json:"registration"`
	Import             *doctor.Batch    `json:"import,omitempty"`
	Name               string           `json:"name,omitempty"`
	StorageAvailable   bool             `json:"storage_available"`
	CredentialsStored  bool             `json:"credentials_stored"`
	CredentialRevision int64            `json:"credential_revision,omitempty"`
}

func newConnections(db *postgres.Store) *connections {
	c := &connections{db: db, checks: map[string]map[string]connectionCheck{}}
	c.vault, _ = credentials.FromEnvironment()
	c.moyAccount = os.Getenv("MOYSKLAD_ACCOUNT_ID")
	c.ozonAccount = os.Getenv("OZON_CLIENT_ID")
	if token := os.Getenv("MOYSKLAD_TOKEN"); token != "" && validConnectionTenant(c.moyAccount) {
		c.moy, _ = moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: token, Timeout: 5 * time.Second, Interval: 400 * time.Millisecond, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	}
	if key := os.Getenv("OZON_API_KEY"); key != "" && validConnectionTenant(c.ozonAccount) {
		c.ozon, _ = ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: c.ozonAccount, APIKey: key, Timeout: 5 * time.Second, Interval: 400 * time.Millisecond, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	}
	return c
}
func (c *connections) account(provider string) (string, bool) {
	switch provider {
	case "moysklad":
		return c.moyAccount, c.moy != nil
	case "ozon":
		return c.ozonAccount, c.ozon != nil
	}
	return "", false
}
func (c *connections) getCheck(session, provider string) (connectionCheck, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.checks[session][provider]
	return v, ok
}
func (c *connections) putCheck(session, provider string, v connectionCheck) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checks == nil {
		c.checks = map[string]map[string]connectionCheck{}
	}
	for k, checks := range c.checks {
		expired := true
		for _, check := range checks {
			if time.Since(check.CheckedAt) < 8*time.Hour {
				expired = false
			}
		}
		if expired {
			delete(c.checks, k)
		}
	}
	// Sessions are capped by the auth layer; discard the oldest completed
	// check if repeated logins leave more retired session entries behind.
	if c.checks[session] == nil && len(c.checks) >= 256 {
		var oldest string
		var at time.Time
		for id, checks := range c.checks {
			var latest time.Time
			for _, check := range checks {
				if check.CheckedAt.After(latest) {
					latest = check.CheckedAt
				}
			}
			if oldest == "" || latest.Before(at) {
				oldest, at = id, latest
			}
		}
		delete(c.checks, oldest)
	}
	if c.checks[session] == nil {
		c.checks[session] = map[string]connectionCheck{}
	}
	c.checks[session][provider] = v
}
func (c *connections) probe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tenant  string `json:"tenant"`
		Account string `json:"account"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	provider := r.PathValue("provider")
	account, configured := c.account(provider)
	if provider != "moysklad" && provider != "ozon" {
		writeError(w, 404, "PROVIDER_NOT_FOUND")
		return
	}
	if (body.Tenant != "" || body.Account != "") && (!validConnectionTenant(body.Tenant) || !validConnectionTenant(body.Account)) {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	session, _ := r.Context().Value(auditContextKey{}).(string)
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	moyClient, ozonClient := c.moy, c.ozon
	checkKey := provider
	if body.Tenant != "" {
		legacyConfigured := configured && body.Account == account
		account = body.Account
		checkKey = scopedCheckKey(body.Tenant, provider, account)
		_, storedErr := c.db.LoadCredential(ctx, credentials.Scope{Tenant: body.Tenant, Provider: provider, Account: account})
		// The existing trusted local operator may test the configured environment
		// account before its separate ownership attestation/registration. A stored
		// record always overrides that fallback, including disabled credentials.
		if legacyConfigured && sessionFrom(r).AuditID == "" && errors.Is(storedErr, credentials.ErrNotFound) {
			configured = true
		} else {
			var err error
			if provider == "moysklad" {
				moyClient, err = c.resolveMoy(ctx, body.Tenant, account)
			} else {
				ozonClient, err = c.resolveOzon(ctx, body.Tenant, account)
			}
			configured = err == nil
		}
	}
	if !configured {
		writeError(w, 409, "NOT_CONFIGURED")
		return
	}
	check := connectionCheck{Status: "VERIFICATION_FAILED", CheckedAt: time.Now().UTC(), Identity: "NOT_ESTABLISHED"}
	var err error
	if provider == "moysklad" {
		var verified bool
		verified, err = moyClient.Probe(ctx, account)
		if verified {
			check.Identity = "MATCHED_RETURNED_PRODUCT"
		}
	} else {
		err = ozonClient.Probe(ctx)
	}
	check.CheckedAt = time.Now().UTC()
	if err == nil {
		check.Status = "VERIFIED"
	} else {
		check.Reason = "PROBE_FAILED"
		var me *moysklad.APIError
		var oe *ozon.APIError
		switch {
		case errors.Is(err, moysklad.ErrProbeAccountMismatch):
			check.Reason = "ACCOUNT_IDENTITY_MISMATCH"
		case errors.As(err, &me) && me.Status == 401, errors.As(err, &oe) && oe.Status == 401:
			check.Reason = "INVALID_CREDENTIALS"
		case errors.As(err, &me) && me.Status == 403, errors.As(err, &oe) && oe.Status == 403:
			check.Reason = "PERMISSION_DENIED"
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			check.Reason = "TIMEOUT"
		case errors.Is(ctx.Err(), context.Canceled):
			check.Reason = "CANCELLED"
		}
	}
	c.putCheck(session, checkKey, check)
	writeJSON(w, check)
}
func (c *connections) list(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")
	if !validConnectionTenant(tenant) {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	session, _ := r.Context().Value(auditContextKey{}).(string)
	var metas []credentials.Metadata
	{
		var err error
		metas, err = c.db.ListCredentialMetadata(r.Context(), tenant)
		if err != nil && !errors.Is(err, credentials.ErrNotFound) {
			writeError(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
	}
	views := make([]providerView, 0, 2)
	for _, provider := range []string{"moysklad", "ozon"} {
		envAccount, envConfigured := c.account(provider)
		if sessionFrom(r).AuditID != "" && envAccount != "" {
			owned, err := c.db.CredentialConnectionOwned(r.Context(), credentials.Scope{Tenant: tenant, Provider: provider, Account: envAccount})
			if err != nil {
				writeError(w, 503, "DATABASE_UNAVAILABLE")
				return
			}
			if !owned {
				envAccount, envConfigured = "", false
			}
		}
		account, configured := envAccount, envConfigured
		selected := r.URL.Query().Get(provider + "_account")
		if selected != "" && !validConnectionTenant(selected) {
			writeError(w, 400, "INVALID_SCOPE")
			return
		}
		if sessionFrom(r).AuditID != "" && selected != "" {
			owned, err := c.db.CredentialConnectionOwned(r.Context(), credentials.Scope{Tenant: tenant, Provider: provider, Account: selected})
			if err != nil {
				writeError(w, 503, "DATABASE_UNAVAILABLE")
				return
			}
			if !owned {
				writeError(w, 404, "NOT_FOUND")
				return
			}
		}
		var stored *credentials.Metadata
		if selected != "" {
			record, err := c.db.LoadCredential(r.Context(), credentials.Scope{Tenant: tenant, Provider: provider, Account: selected})
			if err == nil {
				stored = &record.Metadata
			} else if !errors.Is(err, credentials.ErrNotFound) {
				writeError(w, 503, "DATABASE_UNAVAILABLE")
				return
			}
		} else {
			for _, m := range metas {
				if m.Provider == provider {
					copy := m
					stored = &copy
					break
				}
			}
		}
		if selected != "" {
			account = selected
			configured = envConfigured && selected == envAccount
		}
		if stored != nil {
			account = stored.Account
			configured = stored.Enabled && c.vault != nil
		}
		v := providerView{Provider: provider, Account: account, Status: "NOT_CONFIGURED", Registration: "NOT_CHECKED", StorageAvailable: c.vault != nil}
		if stored != nil {
			v.Name = stored.Name
			v.CredentialsStored = true
			v.CredentialRevision = stored.Revision
		}
		if account != "" {
			v.Registration = "NOT_REGISTERED"
		}
		if configured {
			v.Status = "CONFIGURED_NOT_VERIFIED"
			if stored != nil {
				v.Status = "VERIFIED"
				v.Check = &connectionCheck{Status: "VERIFIED", CheckedAt: stored.CheckedAt, Identity: "AUTHENTICATED_ACCOUNT"}
			}
			check, ok := c.getCheck(session, scopedCheckKey(tenant, provider, account))
			if !ok && stored == nil && account == envAccount {
				check, ok = c.getCheck(session, provider)
			}
			if ok && (stored == nil || !check.CheckedAt.Before(stored.CheckedAt)) {
				v.Status = check.Status
				v.Check = &check
			}
		}
		if account != "" {
			ctx, cancel := context.WithTimeout(r.Context(), c.db.Timeout)
			var owner string
			err := c.db.Pool.QueryRow(ctx, `SELECT tenant_id FROM stock_connections WHERE source=$1 AND account_id=$2`, provider, account).Scan(&owner)
			cancel()
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				writeError(w, 503, "DATABASE_UNAVAILABLE")
				return
			case owner == tenant:
				v.Registration = "REGISTERED"
				scope := postgres.MatchScope{Tenant: tenant}
				if provider == "moysklad" {
					scope.MoyskladAccount = account
				} else {
					scope.OzonAccount = account
				}
				report, err := c.db.Doctor(r.Context(), scope, time.Now(), time.Hour)
				if err != nil {
					writeError(w, 503, "DATABASE_UNAVAILABLE")
					return
				}
				b := report.Batches[provider]
				v.Import = &b
			default:
				v.Registration = "OWNERSHIP_CONFLICT"
			}
		}
		views = append(views, v)
	}
	writeJSON(w, struct {
		Providers []providerView `json:"providers"`
	}{views})
}

func validConnectionTenant(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && len(s) <= 256
}
func (c *connections) register(w http.ResponseWriter, r *http.Request) {
	if sessionFrom(r).AuditID != "" {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	provider := r.PathValue("provider")
	account, configured := c.account(provider)
	if provider != "moysklad" && provider != "ozon" {
		writeError(w, 404, "PROVIDER_NOT_FOUND")
		return
	}
	if !configured {
		writeError(w, 409, "NOT_CONFIGURED")
		return
	}
	var body struct {
		Tenant      string `json:"tenant"`
		Acknowledge bool   `json:"acknowledge"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || d.Decode(&struct{}{}) != io.EOF || !body.Acknowledge || !validConnectionTenant(body.Tenant) {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	session, _ := r.Context().Value(auditContextKey{}).(string)
	// The durable record is checked first, including on retries after an uncertain commit.
	ctx, cancel := context.WithTimeout(r.Context(), c.db.Timeout)
	defer cancel()
	var owner string
	err := c.db.Pool.QueryRow(ctx, `SELECT tenant_id FROM stock_connections WHERE source=$1 AND account_id=$2`, provider, account).Scan(&owner)
	if err == nil {
		if owner != body.Tenant {
			writeError(w, 409, "OWNERSHIP_CONFLICT")
			return
		}
		writeJSON(w, struct {
			Status string `json:"status"`
		}{"ALREADY_REGISTERED"})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "REGISTRATION_OUTCOME_UNKNOWN")
		return
	}
	check, ok := c.getCheck(session, scopedCheckKey(body.Tenant, provider, account))
	if !ok {
		check, ok = c.getCheck(session, provider)
	}
	if !ok || check.Status != "VERIFIED" || time.Since(check.CheckedAt) > 5*time.Minute || time.Since(check.CheckedAt) < 0 {
		writeError(w, 409, "PROBE_REQUIRED")
		return
	}
	err = c.db.RegisterStockConnection(ctx, body.Tenant, provider, account, actorProvenance(r), false)
	if err != nil {
		// A fresh scoped read reconciles an ambiguous commit without retrying a write.
		reconcile, cancelRead := context.WithTimeout(r.Context(), c.db.Timeout)
		defer cancelRead()
		var actual string
		e := c.db.Pool.QueryRow(reconcile, `SELECT tenant_id FROM stock_connections WHERE source=$1 AND account_id=$2`, provider, account).Scan(&actual)
		if e == nil && actual == body.Tenant {
			writeJSON(w, struct {
				Status string `json:"status"`
			}{"REGISTERED"})
			return
		}
		if e == nil && actual != body.Tenant {
			writeError(w, 409, "OWNERSHIP_CONFLICT")
			return
		}
		writeError(w, 503, "REGISTRATION_OUTCOME_UNKNOWN")
		return
	}
	writeJSON(w, struct {
		Status string `json:"status"`
	}{"REGISTERED"})
}
func (a *auth) connectionMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if !a.sameOrigin(r) {
			writeError(w, 403, "FORBIDDEN")
			return
		}
		if err != nil || media != "application/json" {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		next(w, r)
	}
}
