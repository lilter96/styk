package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
)

type credentialAPI struct {
	db *postgres.Store
	c  *connections
}

// registerCredentialRoutes reuses the existing authenticated local-operator and
// same-origin JSON mutation boundary; no production tenant identity is implied.
func registerCredentialRoutes(mux *http.ServeMux, a *auth, db *postgres.Store, c *connections) {
	if c.vault == nil {
		c.vault, _ = credentials.FromEnvironment()
	}
	api := &credentialAPI{db, c}
	mux.HandleFunc("GET /api/integrations/{provider}/credentials", a.authorized(api.get))
	mux.HandleFunc("POST /api/integrations/{provider}/credentials", a.authorized(a.connectionMutation(api.save)))
	mux.HandleFunc("POST /api/integrations/{provider}/disconnect", a.authorized(a.connectionMutation(api.disconnect)))
}
func (c *connections) newMoy(secret string) (*moysklad.Client, error) {
	if c.moyFactory != nil {
		return c.moyFactory(secret)
	}
	return moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: secret, Timeout: 5 * time.Second, Interval: 400 * time.Millisecond, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
}
func (c *connections) newOzon(account, secret string) (*ozon.Client, error) {
	if c.ozonFactory != nil {
		return c.ozonFactory(account, secret)
	}
	return ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: account, APIKey: secret, Timeout: 5 * time.Second, Interval: 400 * time.Millisecond, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
}
func (c *connections) resolveOzon(ctx context.Context, tenant, account string) (*ozon.Client, error) {
	scope := credentials.Scope{Tenant: tenant, Provider: "ozon", Account: account}
	record, err := c.db.LoadCredential(ctx, scope)
	if err == nil {
		key, err := c.vault.Decrypt(record)
		if err != nil {
			return nil, err
		}
		return c.newOzon(account, key)
	}
	if !errors.Is(err, credentials.ErrNotFound) {
		return nil, err
	}
	if c.ozon != nil && c.ozonAccount == account {
		owned, err := c.db.CredentialConnectionOwned(ctx, scope)
		if err != nil || !owned {
			return nil, credentials.ErrOwnership
		}
		return c.ozon, nil
	}
	key, err := (credentials.Resolver{Store: c.db, Vault: c.vault}).Resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	return c.newOzon(account, key)
}
func (c *connections) resolveMoy(ctx context.Context, tenant, account string) (*moysklad.Client, error) {
	scope := credentials.Scope{Tenant: tenant, Provider: "moysklad", Account: account}
	record, err := c.db.LoadCredential(ctx, scope)
	if err == nil {
		key, err := c.vault.Decrypt(record)
		if err != nil {
			return nil, err
		}
		return c.newMoy(key)
	}
	if !errors.Is(err, credentials.ErrNotFound) {
		return nil, err
	}
	if c.moy != nil && c.moyAccount == account {
		owned, err := c.db.CredentialConnectionOwned(ctx, scope)
		if err != nil || !owned {
			return nil, credentials.ErrOwnership
		}
		return c.moy, nil
	}
	key, err := (credentials.Resolver{Store: c.db, Vault: c.vault}).Resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	return c.newMoy(key)
}

func credentialError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, credentials.ErrConflict):
		writeError(w, 409, "IDEMPOTENCY_CONFLICT")
	case errors.Is(err, credentials.ErrVersion):
		writeError(w, 409, "CREDENTIAL_VERSION_CONFLICT")
	case errors.Is(err, credentials.ErrOwnership):
		writeError(w, 409, "OWNERSHIP_CONFLICT")
	case errors.Is(err, credentials.ErrNotFound):
		writeError(w, 404, "CREDENTIAL_NOT_FOUND")
	default:
		writeError(w, 503, "CREDENTIAL_PERSISTENCE_UNKNOWN")
	}
}
func (a *credentialAPI) get(w http.ResponseWriter, r *http.Request) {
	tenant, provider, account := r.URL.Query().Get("tenant"), r.PathValue("provider"), r.URL.Query().Get("account")
	if !validConnectionTenant(tenant) || (provider != "moysklad" && provider != "ozon") || account != "" && !validConnectionTenant(account) {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	var meta *credentials.Metadata
	{
		if account != "" {
			record, err := a.db.LoadCredential(r.Context(), credentials.Scope{Tenant: tenant, Provider: provider, Account: account})
			if err == nil {
				meta = &record.Metadata
			} else if !errors.Is(err, credentials.ErrNotFound) {
				credentialError(w, err)
				return
			}
		} else {
			records, err := a.db.ListCredentialMetadata(r.Context(), tenant)
			if err != nil && !errors.Is(err, credentials.ErrNotFound) {
				credentialError(w, err)
				return
			}
			for _, record := range records {
				if record.Provider == provider {
					copy := record
					meta = &copy
					break
				}
			}
		}
	}
	writeJSON(w, struct {
		StorageAvailable bool                  `json:"storage_available"`
		Credential       *credentials.Metadata `json:"credential"`
	}{a.c.vault != nil, meta})
}

type credentialInput struct {
	Tenant           string `json:"tenant"`
	Account          string `json:"account"`
	Name             string `json:"name"`
	Secret           string `json:"secret"`
	RequestID        string `json:"request_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Acknowledge      bool   `json:"acknowledge"`
}

func (a *credentialAPI) save(w http.ResponseWriter, r *http.Request) {
	if a.c.vault == nil {
		writeError(w, 503, "CREDENTIAL_STORAGE_UNAVAILABLE")
		return
	}
	var in credentialInput
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	provider := r.PathValue("provider")
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || !validConnectionTenant(in.Tenant) || (provider != "moysklad" && provider != "ozon") || !importIdempotencyPattern.MatchString(in.RequestID) || in.ExpectedRevision < 0 || !in.Acknowledge || len(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 120 || strings.TrimSpace(in.Name) != in.Name || strings.ContainsAny(in.Name, "\r\n\x00") || len(in.Secret) < 1 || len(in.Secret) > 8192 || strings.TrimSpace(in.Secret) != in.Secret || strings.ContainsAny(in.Secret, "\r\n\x00") || in.Account != "" && !validConnectionTenant(in.Account) || provider == "ozon" && in.Account == "" {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	raw, _ := json.Marshal(in)
	digest := a.c.vault.Digest(append([]byte("save:"+provider+":"), raw...))
	if meta, err := a.db.CredentialRequest(r.Context(), in.Tenant, provider, in.RequestID, digest); err == nil {
		writeCredentialResult(w, meta, true)
		return
	} else if !errors.Is(err, credentials.ErrNotFound) {
		credentialError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	account := in.Account
	var err error
	if provider == "moysklad" {
		var client *moysklad.Client
		client, err = a.c.newMoy(in.Secret)
		if err == nil {
			var identity string
			identity, err = client.Identity(ctx)
			if err == nil {
				if account != "" && account != identity {
					err = moysklad.ErrProbeAccountMismatch
				} else {
					account = identity
					_, err = client.Probe(ctx, account)
				}
			}
		}
	} else {
		var client *ozon.Client
		client, err = a.c.newOzon(account, in.Secret)
		if err == nil {
			err = client.Probe(ctx)
		}
	}
	if err != nil {
		writeError(w, 422, probeReason(err, ctx))
		return
	}
	scope := credentials.Scope{Tenant: in.Tenant, Provider: provider, Account: account}
	ciphertext, err := a.c.vault.Encrypt(scope, in.ExpectedRevision+1, in.Secret)
	if err != nil {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	session, _ := r.Context().Value(auditContextKey{}).(string)
	meta, replay, err := a.db.SaveCredential(r.Context(), scope, in.Name, in.ExpectedRevision, in.RequestID, digest, ciphertext, time.Now().UTC(), actorProvenance(r))
	if err != nil {
		credentialError(w, err)
		return
	}
	a.c.putCheck(session, scopedCheckKey(in.Tenant, provider, account), connectionCheck{Status: "VERIFIED", CheckedAt: meta.CheckedAt, Identity: "AUTHENTICATED_ACCOUNT"})
	writeCredentialResult(w, meta, replay)
}
func writeCredentialResult(w http.ResponseWriter, m credentials.Metadata, replay bool) {
	writeJSON(w, struct {
		Credential       credentials.Metadata `json:"credential"`
		IdempotentReplay bool                 `json:"idempotent_replay"`
	}{m, replay})
}
func (a *credentialAPI) disconnect(w http.ResponseWriter, r *http.Request) {
	if a.c.vault == nil {
		writeError(w, 503, "CREDENTIAL_STORAGE_UNAVAILABLE")
		return
	}
	var in struct {
		Tenant           string `json:"tenant"`
		Account          string `json:"account"`
		RequestID        string `json:"request_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		Acknowledge      bool   `json:"acknowledge"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	provider := r.PathValue("provider")
	scope := credentials.Scope{Provider: provider}
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || !in.Acknowledge || in.ExpectedRevision < 1 || !importIdempotencyPattern.MatchString(in.RequestID) {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	scope.Tenant, scope.Account = in.Tenant, in.Account
	if !scope.Valid() {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	raw, _ := json.Marshal(in)
	digest := a.c.vault.Digest(append([]byte("disconnect:"+provider+":"), raw...))
	meta, replay, err := a.db.DisconnectCredentialAs(r.Context(), scope, in.ExpectedRevision, in.RequestID, digest, actorProvenance(r))
	if err != nil {
		credentialError(w, err)
		return
	}
	writeCredentialResult(w, meta, replay)
}
func scopedCheckKey(tenant, provider, account string) string {
	raw, _ := json.Marshal([]string{tenant, provider, account})
	return string(raw)
}
func probeReason(err error, ctx context.Context) string {
	var me *moysklad.APIError
	var oe *ozon.APIError
	switch {
	case errors.Is(err, moysklad.ErrProbeAccountMismatch):
		return "ACCOUNT_IDENTITY_MISMATCH"
	case errors.As(err, &me) && me.Status == 401, errors.As(err, &oe) && oe.Status == 401:
		return "INVALID_CREDENTIALS"
	case errors.As(err, &me) && me.Status == 403, errors.As(err, &oe) && oe.Status == 403:
		return "PERMISSION_DENIED"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "PROBE_TIMEOUT"
	default:
		return "PROBE_FAILED"
	}
}
