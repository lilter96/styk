//go:build integration

package console

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/postgres"
)

type credentialFixture struct {
	db                *postgres.Store
	vault             *credentials.Vault
	api               *httptest.Server
	client            *http.Client
	c                 *connections
	tenant, moy, ozon string
	calls             atomic.Int64
}

func newCredentialFixture(t *testing.T) *credentialFixture {
	t.Helper()
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/styk_credentials_v1_test" {
		t.Skip("dedicated local styk_credentials_v1_test database required")
	}
	db, err := postgres.Open(context.Background(), u.String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("v", 32))))
	t.Setenv("OZON_CLIENT_ID", "")
	t.Setenv("OZON_API_KEY", "")
	t.Setenv("MOYSKLAD_ACCOUNT_ID", "")
	t.Setenv("MOYSKLAD_TOKEN", "")
	vault, _ := credentials.FromEnvironment()
	suffix := fmt.Sprint(time.Now().UnixNano())
	f := &credentialFixture{db: db, vault: vault, tenant: "credential-tenant-" + suffix, moy: fmt.Sprintf("00000000-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffffffff), ozon: "credential-ozon-" + suffix}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if strings.Contains(r.Header.Get("Authorization"), "bad") || strings.Contains(r.Header.Get("Api-Key"), "bad") {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/context/employee":
			if r.Method != "GET" {
				t.Error("unsafe identity request")
			}
			fmt.Fprintf(w, `{"accountId":%q}`, f.moy)
		case "/entity/product":
			if r.Method != "GET" || r.URL.Query().Get("limit") != "1" {
				t.Error("unbounded credential probe")
			}
			fmt.Fprintf(w, `{"meta":{"size":1},"rows":[{"id":"p","accountId":%q}]}`, f.moy)
		case "/v3/product/list":
			if r.Method != "POST" {
				t.Error("unexpected catalog verb")
			}
			var body struct {
				Filter struct {
					Visibility string `json:"visibility"`
				} `json:"filter"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid catalog request")
			}
			if body.Filter.Visibility == "ARCHIVED" {
				fmt.Fprint(w, `{"result":{"items":[],"total_items":0}}`)
			} else {
				fmt.Fprint(w, `{"result":{"items":[{"product_id":1,"offer_id":"synthetic-offer","archived":false}],"total_items":1}}`)
			}
		case "/v2/warehouse/list":
			fmt.Fprint(w, `{"warehouses":[{"warehouse_id":10,"name":"Synthetic","status":"created","is_rfbs":false}],"has_next":false}`)
		case "/v2/product/info/stocks-by-warehouse/fbs":
			fmt.Fprint(w, `{"products":[{"product_id":1,"offer_id":"synthetic-offer","warehouse_id":10,"present":3,"reserved":1,"free_stock":2}],"has_next":false}`)
		default:
			t.Error("unallowlisted provider request")
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(mock.Close)
	c := &connections{db: db, vault: vault, checks: map[string]map[string]connectionCheck{}}
	c.moyFactory = func(key string) (*moysklad.Client, error) {
		return moysklad.New(moysklad.Options{BaseURL: mock.URL, Token: key, Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	}
	c.ozonFactory = func(account, key string) (*ozon.Client, error) {
		return ozon.New(ozon.Options{BaseURL: mock.URL, ClientID: account, APIKey: key, Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	}
	f.c = c
	a, err := newAuth(strings.Repeat("s", 32), "http://127.0.0.1:15723")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", a.login)
	registerCredentialRoutes(mux, a, db, c)
	imports := newImportAPI(db)
	mux.HandleFunc("POST /api/imports", a.authorized(a.connectionMutation(imports.create)))
	mux.HandleFunc("GET /api/imports", a.authorized(imports.list))
	mux.HandleFunc("GET /api/integrations", a.authorized(c.list))
	mux.HandleFunc("POST /api/integrations/{provider}/probe", a.authorized(a.connectionMutation(c.probe)))
	mux.HandleFunc("POST /api/integrations/{provider}/register", a.authorized(a.connectionMutation(c.register)))
	f.api = httptest.NewServer(mux)
	t.Cleanup(f.api.Close)
	jar, _ := cookiejar.New(nil)
	f.client = &http.Client{Jar: jar}
	f.request(t, "POST", "/api/session", map[string]string{"secret": strings.Repeat("s", 32)}, 204)
	return f
}
func (f *credentialFixture) request(t *testing.T, method, path string, body any, status int) []byte {
	t.Helper()
	raw, _ := json.Marshal(body)
	r, err := http.NewRequest(method, f.api.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:15723")
	response, err := f.client.Do(r)
	if err != nil {
		t.Fatal("local request failed")
	}
	defer response.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(response.Body)
	if response.StatusCode != status {
		t.Fatalf("request %s %s returned %d, want %d", method, strings.Split(path, "?")[0], response.StatusCode, status)
	}
	for _, secret := range []string{"synthetic-moy-secret", "synthetic-ozon-secret", "synthetic-new-secret", "bad-credential"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("secret exposed by API")
		}
	}
	return out.Bytes()
}
func decodeCredential(t *testing.T, body []byte) (credentials.Metadata, bool) {
	t.Helper()
	var result struct {
		Credential credentials.Metadata `json:"credential"`
		Replay     bool                 `json:"idempotent_replay"`
	}
	if json.Unmarshal(body, &result) != nil {
		t.Fatal("invalid response")
	}
	return result.Credential, result.Replay
}
func TestCredentialStorageIdentityReplayScopeAndStoredWorker(t *testing.T) {
	f := newCredentialFixture(t)
	save := map[string]any{"tenant": f.tenant, "account": "", "name": "Основной склад", "secret": "synthetic-moy-secret", "request_id": "moy-create", "expected_revision": 0, "acknowledge": true}
	m, replay := decodeCredential(t, f.request(t, "POST", "/api/integrations/moysklad/credentials", save, 200))
	if replay || m.Account != f.moy || m.Revision != 1 || !m.Enabled {
		t.Fatal("identity registration failed")
	}
	before := f.calls.Load()
	invalidOrigin, _ := json.Marshal(save)
	req, _ := http.NewRequest("POST", f.api.URL+"/api/integrations/moysklad/credentials", bytes.NewReader(invalidOrigin))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://wrong.invalid")
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal("local origin rejection failed")
	}
	response.Body.Close()
	if response.StatusCode != 403 || f.calls.Load() != before {
		t.Fatal("cross-origin credential mutation reached provider")
	}
	_, replay = decodeCredential(t, f.request(t, "POST", "/api/integrations/moysklad/credentials", save, 200))
	if !replay || f.calls.Load() != before {
		t.Fatal("lost-response replay contacted provider or resubmitted")
	}
	save["name"] = "Другой склад"
	f.request(t, "POST", "/api/integrations/moysklad/credentials", save, 409)
	if f.calls.Load() != before {
		t.Fatal("conflicting request contacted provider")
	}
	save["request_id"] = "stale"
	save["account"] = f.moy
	f.request(t, "POST", "/api/integrations/moysklad/credentials", save, 409)
	save["tenant"] = "other-" + f.tenant
	save["request_id"] = "other-owner"
	f.request(t, "POST", "/api/integrations/moysklad/credentials", save, 409)
	save["tenant"] = f.tenant
	save["request_id"] = "invalid-key"
	save["secret"] = "bad-credential"
	save["expected_revision"] = 1
	f.request(t, "POST", "/api/integrations/moysklad/credentials", save, 422)
	record, err := f.db.LoadCredential(context.Background(), credentials.Scope{Tenant: f.tenant, Provider: "moysklad", Account: f.moy})
	if err != nil {
		t.Fatal(err)
	}
	if record.Revision != 1 {
		t.Fatal("rejected probe altered credential")
	}
	if bytes.Contains(record.Ciphertext, []byte("synthetic-moy-secret")) {
		t.Fatal("plaintext stored")
	}
	if key, err := f.vault.Decrypt(record); err != nil || key != "synthetic-moy-secret" {
		t.Fatal("saved key unavailable")
	}
	f.request(t, "GET", "/api/integrations/moysklad/credentials?tenant="+f.tenant+"&account="+f.moy, nil, 200)
	f.request(t, "POST", "/api/integrations/moysklad/probe", map[string]string{"tenant": f.tenant, "account": f.moy}, 200)
	f.request(t, "GET", "/api/integrations?tenant="+f.tenant+"&moysklad_account="+f.moy, nil, 200)
	ozonSave := map[string]any{"tenant": f.tenant, "account": f.ozon, "name": "Магазин Ozon", "secret": "synthetic-ozon-secret", "request_id": "ozon-create", "expected_revision": 0, "acknowledge": true}
	o, _ := decodeCredential(t, f.request(t, "POST", "/api/integrations/ozon/credentials", ozonSave, 200))
	if o.Revision != 1 {
		t.Fatal("Ozon key not registered")
	}
	importBody := map[string]any{"tenant": f.tenant, "ozon_account": f.ozon, "operation_type": domain.OzonCatalogImport, "idempotency_key": "stored-import", "acknowledge": true}
	f.request(t, "POST", "/api/imports", importBody, 201)
	task, err := f.db.ClaimForCredentialWorker(context.Background(), 5*time.Second, "", "")
	if err != nil || task == nil {
		t.Fatal("stored-account job was not claimed", err)
	}
	resolver := credentials.Resolver{Store: f.db, Vault: f.vault}
	runner := ozonjob.Runner{Store: f.db, Resolver: &resolver, NewOzon: f.c.ozonFactory}
	if err = runner.ProcessImport(context.Background(), *task, 5*time.Second); err != nil {
		t.Fatal("stored-account worker failed", err)
	}
	op, err := f.db.GetCatalogOperation(context.Background(), f.tenant, "ozon", f.ozon, task.ImportOperationID)
	if err != nil || op.State != "SUCCEEDED" || op.BatchID == nil {
		t.Fatal("stored credential import did not persist", err)
	}
	_, _, err = f.db.SubmitCatalogImport(context.Background(), f.tenant, "ozon", f.ozon, "queued-before-disconnect", "synthetic", domain.DefaultOzonImportLimits())
	if err != nil {
		t.Fatal("cannot queue guarded import")
	}
	disconnect := map[string]any{"tenant": f.tenant, "account": f.ozon, "request_id": "disconnect", "expected_revision": 1, "acknowledge": true}
	disabled, _ := decodeCredential(t, f.request(t, "POST", "/api/integrations/ozon/disconnect", disconnect, 200))
	if disabled.Enabled || disabled.Revision != 2 {
		t.Fatal("disconnect did not advance version")
	}
	_, replay = decodeCredential(t, f.request(t, "POST", "/api/integrations/ozon/disconnect", disconnect, 200))
	if !replay {
		t.Fatal("disconnect replay not recovered")
	}
	t.Setenv("OZON_CLIENT_ID", f.ozon)
	t.Setenv("OZON_API_KEY", "synthetic-ozon-secret")
	if _, err = resolver.Resolve(context.Background(), credentials.Scope{Tenant: f.tenant, Provider: "ozon", Account: f.ozon}); !errors.Is(err, credentials.ErrUnavailable) {
		t.Fatal("disconnected key fell back to environment")
	}
	withoutKey := credentials.Resolver{Store: f.db}
	if _, err = withoutKey.Resolve(context.Background(), credentials.Scope{Tenant: f.tenant, Provider: "ozon", Account: f.ozon}); !errors.Is(err, credentials.ErrUnavailable) {
		t.Fatal("missing master key bypassed disconnect")
	}
	legacyTask, err := f.db.ClaimForCatalogWorker(context.Background(), 5*time.Second, f.ozon, "")
	if err != nil || legacyTask == nil {
		t.Fatal("queued legacy import unavailable")
	}
	callsBefore := f.calls.Load()
	legacyRunner := ozonjob.Runner{Store: f.db, Resolver: &withoutKey, NewOzon: f.c.ozonFactory}
	if err = legacyRunner.ProcessImport(context.Background(), *legacyTask, 5*time.Second); err != nil {
		t.Fatal("disconnect guard did not persist", err)
	}
	if f.calls.Load() != callsBefore {
		t.Fatal("disconnected queued import contacted provider")
	}
	guarded, err := f.db.GetCatalogOperation(context.Background(), f.tenant, "ozon", f.ozon, legacyTask.ImportOperationID)
	if err != nil || guarded.State != "FAILED" {
		t.Fatal("queued disconnected import not safely rejected")
	}
	savedVault := f.c.vault
	f.c.vault = nil
	f.c.ozonAccount = f.ozon
	f.c.ozon, _ = f.c.ozonFactory(f.ozon, "synthetic-ozon-secret")
	if _, err = f.c.resolveOzon(context.Background(), f.tenant, f.ozon); !errors.Is(err, credentials.ErrUnavailable) {
		t.Fatal("missing API master key bypassed disconnect")
	}
	f.c.vault = savedVault

	stored, err := f.db.LoadCredential(context.Background(), credentials.Scope{Tenant: f.tenant, Provider: "ozon", Account: f.ozon})
	if err != nil || stored.Ciphertext != nil {
		t.Fatal("disconnect retained encrypted material")
	}
	f.request(t, "POST", "/api/imports", importBody, 200)
	importBody["idempotency_key"] = "after-disconnect"
	f.request(t, "POST", "/api/imports", importBody, 403)
	f.request(t, "GET", "/api/imports?tenant="+f.tenant+"&ozon_account="+f.ozon+"&limit=10&offset=0", nil, 200)
}
func TestCredentialConcurrentRevisionAndRegistrationRollback(t *testing.T) {
	f := newCredentialFixture(t)
	scope := credentials.Scope{Tenant: f.tenant, Provider: "ozon", Account: f.ozon}
	ciphertext, _ := f.vault.Encrypt(scope, 1, "synthetic-ozon-secret")
	_, _, err := f.db.SaveCredential(context.Background(), scope, "One", 0, "initial", f.vault.Digest([]byte("initial")), ciphertext, time.Now(), "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"left", "right"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			encrypted, _ := f.vault.Encrypt(scope, 2, "synthetic-new-secret")
			_, _, e := f.db.SaveCredential(context.Background(), scope, id, 1, id, f.vault.Digest([]byte(id)), encrypted, time.Now(), "synthetic")
			results <- e
		}(id)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, credentials.ErrVersion) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent updates were not serialized")
	}
	if _, err = f.db.Pool.Exec(context.Background(), `UPDATE credential_save_requests SET name='tampered' WHERE tenant_id=$1`, f.tenant); err == nil {
		t.Fatal("request ledger mutated")
	}
	if _, err = f.db.Pool.Exec(context.Background(), `UPDATE connection_credentials SET name='tampered' WHERE tenant_id=$1`, f.tenant); err == nil {
		t.Fatal("credential revision guard bypassed")
	}

	other := scope
	other.Account += "-rollback"
	encrypted, _ := f.vault.Encrypt(other, 2, "synthetic-new-secret")
	_, _, err = f.db.SaveCredential(context.Background(), other, "Rollback", 1, "rollback", f.vault.Digest([]byte("rollback")), encrypted, time.Now(), "synthetic")
	if !errors.Is(err, credentials.ErrVersion) {
		t.Fatal("invalid expected revision accepted")
	}
	owned, err := f.db.CredentialConnectionOwned(context.Background(), other)
	if err != nil || owned {
		t.Fatal("failed credential write registered the account")
	}
}

func TestCredentialLegacyScopedProbeBeforeExplicitRegistration(t *testing.T) {
	f := newCredentialFixture(t)
	f.c.ozonAccount = f.ozon
	f.c.ozon, _ = f.c.ozonFactory(f.ozon, "synthetic-env-secret")
	path := "/api/integrations/ozon/"
	f.request(t, "POST", path+"register", map[string]any{"tenant": f.tenant, "acknowledge": true}, 409)
	f.request(t, "POST", path+"probe", map[string]string{"tenant": f.tenant, "account": f.ozon}, 200)
	body := f.request(t, "GET", "/api/integrations?tenant="+f.tenant+"&ozon_account="+f.ozon, nil, 200)
	var view struct {
		Providers []providerView `json:"providers"`
	}
	if json.Unmarshal(body, &view) != nil {
		t.Fatal("invalid connection view")
	}
	var found bool
	for _, p := range view.Providers {
		if p.Provider == "ozon" {
			found = p.Status == "VERIFIED" && p.Registration == "NOT_REGISTERED"
		}
	}
	if !found {
		t.Fatal("scoped probe did not preserve verified, unregistered state")
	}
	f.request(t, "POST", path+"register", map[string]any{"tenant": f.tenant, "acknowledge": true}, 200)
	scope := credentials.Scope{Tenant: f.tenant, Provider: "ozon", Account: f.ozon}
	owned, err := f.db.CredentialConnectionOwned(context.Background(), scope)
	if err != nil || !owned {
		t.Fatal("explicit legacy registration failed")
	}
	if _, err := f.db.LoadCredential(context.Background(), scope); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("legacy probe unexpectedly persisted provider credentials")
	}
}
