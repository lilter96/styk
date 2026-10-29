//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/postgres"
)

type testMailbox struct {
	mu     sync.Mutex
	tokens map[string]string
}

func (m *testMailbox) Send(_ context.Context, recipient, purpose, link string) error {
	u, err := url.Parse(link)
	if err != nil {
		return err
	}
	parts := strings.SplitN(u.Fragment, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("missing test token")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[recipient+":"+purpose] = parts[1]
	return nil
}

func (m *testMailbox) token(recipient, purpose string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokens[recipient+":"+purpose]
}

type merchantBrowser struct {
	client *http.Client
	base   string
	csrf   string
}

type merchantSession struct {
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	CSRF      string `json:"csrf"`
	AccountID string `json:"account_id"`
}

func newMerchantBrowser(t *testing.T, server string) *merchantBrowser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &merchantBrowser{client: &http.Client{Jar: jar, Timeout: 15 * time.Second}, base: server}
}

func (b *merchantBrowser) request(t *testing.T, method, path, body string, csrf bool) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, b.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	// A local reverse proxy preserves the public browser Host header.
	req.Host = "127.0.0.1:5173"
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://127.0.0.1:5173")
		if csrf {
			req.Header.Set("X-STYK-CSRF", b.csrf)
		}
	}
	resp, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}

func (b *merchantBrowser) registerAndLogin(t *testing.T, mailbox *testMailbox, email string) merchantSession {
	t.Helper()
	body, err := json.Marshal(map[string]string{"email": email})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := b.request(t, http.MethodPost, "/api/registration", string(body), false); status != 202 {
		t.Fatalf("registration status %d", status)
	}
	login, err := json.Marshal(map[string]string{"email": email, "password": "long-merchant-password"})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := b.request(t, http.MethodPost, "/api/session", string(login), false); status != 401 {
		t.Fatalf("unverified registration authenticated: %d", status)
	}
	token := mailbox.token(email, "VERIFY_EMAIL")
	verification, err := json.Marshal(map[string]string{"token": token, "password": "long-merchant-password"})
	if err != nil || token == "" {
		t.Fatal("verification not delivered")
	}
	if status, _ := b.request(t, http.MethodPost, "/api/verification", string(verification), false); status != 204 {
		t.Fatalf("verification status %d", status)
	}
	if status, _ := b.request(t, http.MethodPost, "/api/session", string(login), false); status != 204 {
		t.Fatalf("login status %d", status)
	}
	status, raw := b.request(t, http.MethodGet, "/api/session", "", false)
	var session merchantSession
	if status != 200 || json.Unmarshal(raw, &session) != nil || session.AccountID == "" || session.CSRF == "" || strings.Contains(string(raw), "workspaces") {
		t.Fatal("invalid merchant session")
	}
	b.csrf = session.CSRF
	return session
}

func TestMerchantHTTPWorkspaceAndObjectIsolation(t *testing.T) {
	db, _ := controlledFixture(t)
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	// An operator environment may still contain credentials for a legacy
	// account. A new workspace must neither list nor probe that account.
	t.Setenv("OZON_CLIENT_ID", "legacy-operator-ozon")
	t.Setenv("OZON_API_KEY", "synthetic-legacy-ozon-secret")
	t.Setenv("MOYSKLAD_ACCOUNT_ID", "legacy-operator-moy")
	t.Setenv("MOYSKLAD_TOKEN", "synthetic-legacy-moy-secret")
	mailbox := &testMailbox{tokens: map[string]string{}}
	handler, err := console.MerchantHandler(db, "http://127.0.0.1:5173", mailbox)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	a, b := newMerchantBrowser(t, server.URL), newMerchantBrowser(t, server.URL)
	sa := a.registerAndLogin(t, mailbox, "merchant-a@example.test")
	sb := b.registerAndLogin(t, mailbox, "merchant-b@example.test")
	wa, wb := sa.AccountID, sb.AccountID
	if wa == wb {
		t.Fatal("merchants share an account")
	}
	assertMerchantIsolation(t, a, b, wa, wb)
	assertLegacyCredentialsHidden(t, b, wb)
	assertMerchantObjects(t, db, a, b, wa, wb)
}

func assertLegacyCredentialsHidden(t *testing.T, browser *merchantBrowser, workspace string) {
	t.Helper()
	status, raw := browser.request(t, http.MethodGet, "/api/integrations?tenant="+url.QueryEscape(workspace), "", false)
	if status != 200 || strings.Contains(string(raw), "legacy-operator-") || strings.Contains(string(raw), "synthetic-legacy-") {
		t.Fatalf("legacy environment credentials exposed to merchant: status %d", status)
	}
	body := `{"tenant":"` + workspace + `","account":"legacy-operator-ozon"}`
	if status, _ = browser.request(t, http.MethodPost, "/api/integrations/ozon/probe", body, true); status != 409 {
		t.Fatalf("legacy environment account probe accepted for new workspace: %d", status)
	}
}

func assertMerchantIsolation(t *testing.T, a, b *merchantBrowser, wa, wb string) {
	t.Helper()
	for _, tc := range []struct {
		browser *merchantBrowser
		tenant  string
	}{{a, wb}, {b, wa}} {
		query := "?tenant=" + url.QueryEscape(tc.tenant) + "&moysklad_account=synthetic-moy&ozon_account=synthetic-ozon"
		for _, path := range []string{"/api/registrations", "/api/products", "/api/stock-sync", "/api/stock-sync/automatic", "/api/stock-sync/order-evidence"} {
			status, _ := tc.browser.request(t, http.MethodGet, path+query, "", false)
			if status != 404 {
				t.Fatalf("cross-tenant read %s status %d", path, status)
			}
		}
		for _, path := range []string{"/api/imports", "/api/product-mapping-reviews", "/api/warehouse-mapping-reviews", "/api/stock-sync/automatic", "/api/stock-sync/enable", "/api/stock-sync/operations/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/supersede", "/api/stock-sync/operations/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/reconcile"} {
			status, _ := tc.browser.request(t, http.MethodPost, path, `{"tenant":"`+tc.tenant+`","moysklad_account":"synthetic-moy","ozon_account":"synthetic-ozon"}`, true)
			if status != 404 {
				t.Fatalf("cross-tenant mutation %s status %d", path, status)
			}
		}
	}
	if status, _ := a.request(t, http.MethodGet, "/api/workspaces/"+wb+"/audit", "", false); status != 404 {
		t.Fatalf("foreign audit visible: %d", status)
	}
	if status, _ := b.request(t, http.MethodGet, "/api/workspaces/"+wb+"/audit", "", false); status != 404 {
		t.Fatalf("obsolete workspace API remains available: %d", status)
	}
	if status, _ := a.request(t, http.MethodGet, "/api/registrations", "", false); status != 200 {
		t.Fatalf("own account was not derived from the session: %d", status)
	}
	if status, _ := a.request(t, http.MethodPost, "/api/imports", `{"tenant":"`+wb+`"}`, true); status != 404 {
		t.Fatalf("cross-tenant mutation status %d", status)
	}
	if status, _ := a.request(t, http.MethodPost, "/api/imports", `{"tenant":"`+wa+`","Tenant":"`+wb+`"}`, true); status != 404 {
		t.Fatalf("ambiguous tenant accepted: %d", status)
	}
	if status, _ := a.request(t, http.MethodPost, "/api/imports", `{"tenant":"`+wa+`"}`, false); status != 403 {
		t.Fatalf("missing CSRF accepted: %d", status)
	}
}

func assertMerchantObjects(t *testing.T, db *postgres.Store, a, b *merchantBrowser, wa, wb string) {
	t.Helper()
	ctx := context.Background()
	for _, entry := range []struct{ tenant, account string }{{wa, "merchant-a-ozon"}, {wb, "merchant-b-ozon"}} {
		if err := db.RegisterStockConnection(ctx, entry.tenant, "ozon", entry.account, "synthetic", false); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RegisterStockConnection(ctx, wb, "ozon", "merchant-b-second-ozon", "synthetic", false); err != nil {
		t.Fatal(err)
	}
	status, own := b.request(t, http.MethodGet, "/api/registrations", "", false)
	if status != 200 || !strings.Contains(string(own), "merchant-b-ozon") || !strings.Contains(string(own), "merchant-b-second-ozon") {
		t.Fatalf("second connection unavailable to its merchant: %d", status)
	}
	status, foreign := a.request(t, http.MethodGet, "/api/registrations", "", false)
	if status != 200 || strings.Contains(string(foreign), "merchant-b-second-ozon") {
		t.Fatalf("second connection visible to another merchant: %d", status)
	}
	vault, err := credentials.FromEnvironment()
	if err != nil || vault == nil {
		t.Fatal("synthetic credential vault unavailable")
	}
	credentialScope := credentials.Scope{Tenant: wb, Provider: "ozon", Account: "merchant-b-ozon"}
	const secret = "synthetic-b-secret-never-returned"
	ciphertext, err := vault.Encrypt(credentialScope, 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.SaveCredential(ctx, credentialScope, "Merchant B", 0, "merchant-b-credential", vault.Digest([]byte("synthetic")), ciphertext, time.Now(), "synthetic"); err != nil {
		t.Fatal(err)
	}
	credentialPath := "/api/integrations/ozon/credentials?tenant="
	status, raw := b.request(t, http.MethodGet, credentialPath+url.QueryEscape(wb)+"&account=merchant-b-ozon", "", false)
	if status != 200 || strings.Contains(string(raw), secret) || !strings.Contains(string(raw), "Merchant B") {
		t.Fatalf("merchant credential metadata response was unsafe or missing: %d", status)
	}
	status, raw = a.request(t, http.MethodGet, credentialPath+url.QueryEscape(wa)+"&account=merchant-b-ozon", "", false)
	if status != 200 || strings.Contains(string(raw), "Merchant B") || strings.Contains(string(raw), secret) {
		t.Fatalf("foreign credential visible under another tenant: %d", status)
	}
	if status, _ = a.request(t, http.MethodGet, credentialPath+url.QueryEscape(wb)+"&account=merchant-b-ozon", "", false); status != 404 {
		t.Fatalf("foreign credential endpoint accepted: %d", status)
	}
	op, _, err := db.SubmitCatalogImport(ctx, wb, "ozon", "merchant-b-ozon", "synthetic-import-key", "synthetic-session", domain.DefaultOzonImportLimits())
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/imports/" + op.ID + "?tenant=" + url.QueryEscape(wb) + "&ozon_account=merchant-b-ozon"
	if status, _ := b.request(t, http.MethodGet, path, "", false); status != 200 {
		t.Fatalf("owner import status %d", status)
	}
	if status, _ := a.request(t, http.MethodGet, path, "", false); status != 404 {
		t.Fatalf("foreign import visible: %d", status)
	}
	forged := "/api/imports/" + op.ID + "?tenant=" + url.QueryEscape(wa) + "&ozon_account=merchant-b-ozon"
	if status, _ := a.request(t, http.MethodGet, forged, "", false); status != 404 {
		t.Fatalf("foreign account accepted: %d", status)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO ozon_posting_batches(tenant_id,account_id,window_from,window_to,started_at,finished_at,traversal_status)
	 VALUES($1,'merchant-b-ozon',clock_timestamp()-interval '1 hour',clock_timestamp(),clock_timestamp(),clock_timestamp(),'completed')`, wa)
	if err == nil {
		t.Fatal("foreign posting account accepted for active workspace")
	}
	assertMerchantMapping(t, db, a, b, wb)
	foreignProducts := "/api/products?tenant=" + url.QueryEscape(wa) + "&moysklad_account=merchant-b-moy&ozon_account=merchant-b-ozon"
	if status, _ = a.request(t, http.MethodGet, foreignProducts, "", false); status != 404 {
		t.Fatalf("foreign product account accepted: %d", status)
	}
}

func assertMerchantMapping(t *testing.T, db *postgres.Store, attacker, owner *merchantBrowser, workspace string) {
	t.Helper()
	ctx := context.Background()
	if err := db.RegisterStockConnection(ctx, workspace, "moysklad", "merchant-b-moy", "synthetic", false); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	archived := false
	source := domain.InventoryImport{Account: "merchant-b-moy", StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "merchant-b-product", Article: "B-OFFER"}}}
	market := domain.OzonImport{Account: "merchant-b-ozon", StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "101", OfferID: "B-OFFER", SKU: "201", Archived: &archived}}}
	if _, err := db.SaveInventoryImport(ctx, source, "merchant-b-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "merchant-b-market"); err != nil {
		t.Fatal(err)
	}
	// The merchant does not send a tenant; the authenticated account is bound
	// before the existing mapping service receives the request.
	body := `{"ozon_account":"merchant-b-ozon","moysklad_account":"merchant-b-moy","product_id":"101","target_kind":"product","target_id":"merchant-b-product","evidence_class":"OPERATOR_ATTESTED"}`
	status, raw := owner.request(t, http.MethodPost, "/api/product-mapping-reviews", body, true)
	var review struct {
		Review struct {
			ID string `json:"id"`
		} `json:"review"`
	}
	if status != 201 || json.Unmarshal(raw, &review) != nil || review.Review.ID == "" {
		t.Fatalf("create merchant mapping review: status=%d", status)
	}
	path := "/api/product-mapping-reviews/" + review.Review.ID
	if status, _ := attacker.request(t, http.MethodGet, path, "", false); status != 404 {
		t.Fatalf("foreign review visible: %d", status)
	}
	confirm := `{"idempotency_key":"merchant-map-confirm","acknowledge":true,"explanation":"Synthetic mapping"}`
	if status, _ := attacker.request(t, http.MethodPost, path+"/confirm", confirm, true); status != 404 {
		t.Fatalf("foreign review confirmation accepted: %d", status)
	}
	if status, _ := owner.request(t, http.MethodPost, path+"/confirm", confirm, true); status != 200 {
		t.Fatalf("merchant confirmation status %d", status)
	}
	var principal string
	if err := db.Pool.QueryRow(ctx, `SELECT principal_type FROM product_mapping_confirmation_audits WHERE review_id=$1`, review.Review.ID).Scan(&principal); err != nil || principal != "MERCHANT_SESSION" {
		t.Fatalf("merchant audit principal %q: %v", principal, err)
	}
}

func TestMerchantCannotReadOrRecoverForeignPublication(t *testing.T) {
	db, review := controlledFixture(t)
	operation := controlledEnable(t, db, review).LastOperation
	ctx := context.Background()
	claimed, err := db.NextControlledStockPublication(ctx, time.Now().UTC())
	if err != nil || claimed == nil {
		t.Fatalf("claim synthetic publication: %v", err)
	}
	if err = db.RecoverControlledStockPublications(ctx, time.Now().UTC().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	store, err := identity.New(db.Pool, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	mailbox := &testMailbox{tokens: map[string]string{}}
	handler, err := console.MerchantHandler(db, "http://127.0.0.1:5173", mailbox)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	owner, attacker := newMerchantBrowser(t, server.URL), newMerchantBrowser(t, server.URL)
	ownerSession := owner.registerAndLogin(t, mailbox, "publication-owner@example.test")
	attacker.registerAndLogin(t, mailbox, "publication-attacker@example.test")
	if err = store.ClaimLegacy(ctx, review.Tenant, ownerSession.User.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.BindLegacyMerchant(ctx, ownerSession.User.ID, review.Tenant); err != nil {
		t.Fatal(err)
	}
	login := `{"email":"publication-owner@example.test","password":"long-merchant-password"}`
	if status, _ := owner.request(t, http.MethodPost, "/api/session", login, false); status != 204 {
		t.Fatalf("owner login after migration: %d", status)
	}
	status, raw := owner.request(t, http.MethodGet, "/api/session", "", false)
	if status != 200 || json.Unmarshal(raw, &ownerSession) != nil || ownerSession.AccountID != review.Tenant {
		t.Fatal("migrated owner account unavailable")
	}
	owner.csrf = ownerSession.CSRF
	q := "?moysklad_account=" + url.QueryEscape(review.MoyskladAccount) + "&ozon_account=" + url.QueryEscape(review.OzonAccount)
	status, raw = owner.request(t, http.MethodGet, "/api/stock-sync"+q, "", false)
	if status != 200 || !strings.Contains(string(raw), operation.ID) || !strings.Contains(string(raw), "UNKNOWN") {
		t.Fatalf("owner cannot inspect own unresolved publication: %d", status)
	}
	status, raw = attacker.request(t, http.MethodGet, "/api/stock-sync"+q, "", false)
	if status == 200 && strings.Contains(string(raw), operation.ID) {
		t.Fatal("foreign publication visible without a tenant parameter")
	}
	foreign := `{"tenant":"` + review.Tenant + `","moysklad_account":"` + review.MoyskladAccount + `","ozon_account":"` + review.OzonAccount + `","acknowledge":true}`
	for _, action := range []string{"reconcile", "supersede"} {
		path := "/api/stock-sync/operations/" + operation.ID + "/" + action
		if status, _ = attacker.request(t, http.MethodPost, path, foreign, true); status != 404 {
			t.Fatalf("foreign %s accepted: %d", action, status)
		}
		ownScope := `{"moysklad_account":"` + review.MoyskladAccount + `","ozon_account":"` + review.OzonAccount + `","acknowledge":true}`
		if status, _ = attacker.request(t, http.MethodPost, path, ownScope, true); status < 400 {
			t.Fatalf("foreign %s accepted with inferred tenant: %d", action, status)
		}
	}
	persisted, err := db.StockPublication(ctx, review.StockSyncScope, operation.ID)
	if err != nil || persisted.State != "UNKNOWN" {
		t.Fatalf("foreign request changed historical outcome: %+v %v", persisted, err)
	}
}
