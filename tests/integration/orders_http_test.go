//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type ordersHTTPBrowser struct {
	client *http.Client
	apis   []*ordersProcessChild
	csrf   string
	scope  orders.Scope
	config orders.Config
}

func ordersHTTPNewBrowser(t *testing.T, apis []*ordersProcessChild) *ordersHTTPBrowser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &ordersHTTPBrowser{client: &http.Client{Jar: jar, Timeout: 15 * time.Second}, apis: apis}
}

func (b *ordersHTTPBrowser) request(t *testing.T, replica int, method, path string, body any, csrf bool) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, b.apis[replica].url+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = strings.TrimPrefix(ordersProcessOrigin, "http://")
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", ordersProcessOrigin)
		if csrf {
			req.Header.Set("X-STYK-CSRF", b.csrf)
		}
	}
	response, err := b.client.Do(req)
	if err != nil {
		t.Fatal("request real orders API replica")
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error("close orders API response")
		}
	}()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func (b *ordersHTTPBrowser) register(t *testing.T, mailbox *testMailbox, email string) merchantSession {
	t.Helper()
	if status, _ := b.request(t, 0, http.MethodPost, "/api/registration", map[string]string{"email": email}, false); status != http.StatusAccepted {
		t.Fatalf("register through API 1: %d", status)
	}
	login := map[string]string{"email": email, "password": "synthetic-orders-process-password"}
	if status, _ := b.request(t, 1, http.MethodPost, "/api/session", login, false); status != http.StatusUnauthorized {
		t.Fatalf("unverified login through API 2: %d", status)
	}
	token := mailbox.token(email, "VERIFY_EMAIL")
	if token == "" {
		t.Fatal("loopback STARTTLS SMTP verification was not delivered")
	}
	if status, _ := b.request(t, 1, http.MethodPost, "/api/verification", map[string]string{"token": token, "password": login["password"]}, false); status != http.StatusNoContent {
		t.Fatalf("verify through API 2: %d", status)
	}
	return b.login(t, email)
}

func (b *ordersHTTPBrowser) login(t *testing.T, email string) merchantSession {
	t.Helper()
	login := map[string]string{"email": email, "password": "synthetic-orders-process-password"}
	if status, _ := b.request(t, 2, http.MethodPost, "/api/session", login, false); status != http.StatusNoContent {
		t.Fatalf("login through API 3: %d", status)
	}
	var session merchantSession
	for replica := range b.apis {
		status, raw := b.request(t, replica, http.MethodGet, "/api/session", nil, false)
		if status != http.StatusOK || json.Unmarshal(raw, &session) != nil || session.AccountID == "" || session.CSRF == "" {
			t.Fatalf("shared session unavailable through API %d: %d", replica+1, status)
		}
		b.csrf = session.CSRF
	}
	return session
}

func (b *ordersHTTPBrowser) saveConfig(t *testing.T, config orders.Config) {
	t.Helper()
	body := ordersHTTPBody(config.Scope)
	body["config"], body["expected_revision"], body["idempotency_key"] = config, b.config.Revision, ordersProcessRandom(t)
	status, raw := b.request(t, 0, http.MethodPost, "/api/orders/config", body, true)
	if status != http.StatusOK || json.Unmarshal(raw, &b.config) != nil {
		t.Fatalf("save orders config through API 1: %d", status)
	}
	status, raw = b.request(t, 1, http.MethodGet, "/api/orders/config"+ordersHTTPQuery(b.scope), nil, false)
	var observed orders.Config
	if status != http.StatusOK || json.Unmarshal(raw, &observed) != nil || observed.Revision != b.config.Revision {
		t.Fatalf("API 2 did not observe API 1 durable config: %d", status)
	}
}

func ordersHTTPBody(scope orders.Scope) map[string]any {
	return map[string]any{"tenant": scope.Tenant, "ozon_account": scope.OzonAccount, "moysklad_account": scope.MoyskladAccount}
}

func ordersHTTPQuery(scope orders.Scope) string {
	return "?" + url.Values{"tenant": {scope.Tenant}, "ozon_account": {scope.OzonAccount}, "moysklad_account": {scope.MoyskladAccount}}.Encode()
}

func (b *ordersHTTPBrowser) check(t *testing.T) {
	t.Helper()
	if status, _ := b.request(t, 2, http.MethodPost, "/api/orders/check", ordersHTTPBody(b.scope), true); status != http.StatusOK {
		t.Fatalf("schedule orders check through API 3: %d", status)
	}
}

func (b *ordersHTTPBrowser) preview(t *testing.T, numbers ...string) orders.Preview {
	t.Helper()
	body := ordersHTTPBody(b.scope)
	body["expected_revision"], body["posting_numbers"] = b.config.Revision, numbers
	status, raw := b.request(t, 0, http.MethodPost, "/api/orders/preview", body, true)
	var preview orders.Preview
	if status != http.StatusOK || json.Unmarshal(raw, &preview) != nil {
		t.Fatalf("preview through API 1: %d", status)
	}
	return preview
}

func (b *ordersHTTPBrowser) approve(t *testing.T, preview orders.Preview) orders.Operation {
	t.Helper()
	body := ordersHTTPBody(b.scope)
	body["preview_id"], body["idempotency_key"], body["acknowledge"] = preview.ID, "approve-"+preview.ID, true
	status, raw := b.request(t, 1, http.MethodPost, "/api/orders/approve", body, true)
	var operation orders.Operation
	if status != http.StatusOK || json.Unmarshal(raw, &operation) != nil {
		t.Fatalf("approve through API 2: %d", status)
	}
	return operation
}

func ordersHTTPIsolation(t *testing.T, a, b *ordersHTTPBrowser, guessedPreview string) {
	t.Helper()
	for _, pair := range []struct{ actor, foreign *ordersHTTPBrowser }{{a, b}, {b, a}} {
		for _, path := range []string{"config", "postings", "operations", "metadata", "metadata/registry"} {
			status, raw := pair.actor.request(t, 2, http.MethodGet, "/api/orders/"+path+ordersHTTPQuery(pair.foreign.scope), nil, false)
			if status != http.StatusNotFound || strings.Contains(string(raw), "synthetic-orders-") {
				t.Fatalf("foreign orders %s read: %d", path, status)
			}
		}
		for _, path := range []string{"config", "preview", "approve", "check", "metadata/attributes"} {
			body := ordersHTTPBody(pair.foreign.scope)
			body["preview_id"], body["acknowledge"], body["idempotency_key"] = guessedPreview, true, "cross-tenant"
			status, raw := pair.actor.request(t, 1, http.MethodPost, "/api/orders/"+path, body, true)
			if status != http.StatusNotFound || strings.Contains(string(raw), "synthetic-orders-") {
				t.Fatalf("foreign orders %s mutation: %d", path, status)
			}
		}
	}
	body := ordersHTTPBody(b.scope)
	body["preview_id"], body["acknowledge"], body["idempotency_key"] = guessedPreview, true, "guessed-object"
	if status, _ := b.request(t, 2, http.MethodPost, "/api/orders/approve", body, true); status != http.StatusNotFound {
		t.Fatalf("guessed foreign preview accepted in own scope: %d", status)
	}
	if status, _ := a.request(t, 2, http.MethodPost, "/api/orders/check", ordersHTTPBody(a.scope), false); status != http.StatusForbidden {
		t.Fatalf("mutation without cross-replica CSRF: %d", status)
	}
	metadata := ordersHTTPBody(a.scope)
	metadata["unique_code"], metadata["type"], metadata["idempotency_key"] = "styk-order-status", "string", "explicit-metadata-confirmation"
	if status, _ := a.request(t, 2, http.MethodPost, "/api/orders/metadata/attributes", metadata, true); status != http.StatusBadRequest {
		t.Fatalf("metadata setup without explicit confirmation: %d", status)
	}
	metadata["acknowledge"] = true
	if status, _ := a.request(t, 2, http.MethodPost, "/api/orders/metadata/attributes", metadata, false); status != http.StatusForbidden {
		t.Fatalf("metadata setup without cross-replica CSRF: %d", status)
	}
}
