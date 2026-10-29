package console

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
)

func TestConnectionConfigurationUsesNoNetwork(t *testing.T) {
	for _, key := range []string{"MOYSKLAD_TOKEN", "MOYSKLAD_ACCOUNT_ID", "OZON_CLIENT_ID", "OZON_API_KEY"} {
		t.Setenv(key, "")
	}
	c := newConnections(nil)
	for _, provider := range []string{"moysklad", "ozon"} {
		if _, ok := c.account(provider); ok {
			t.Fatalf("%s configured without credentials", provider)
		}
	}
	t.Setenv("MOYSKLAD_TOKEN", "synthetic-token")
	t.Setenv("MOYSKLAD_ACCOUNT_ID", "synthetic-account")
	t.Setenv("OZON_CLIENT_ID", "synthetic-client")
	t.Setenv("OZON_API_KEY", "synthetic-key")
	c = newConnections(nil)
	for _, provider := range []string{"moysklad", "ozon"} {
		if _, ok := c.account(provider); !ok {
			t.Fatalf("%s is not configured", provider)
		}
	}
}

func TestProbeDoesNotExposeSecretsInResultsOrLogs(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	const credential = "synthetic-never-log-this-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, credential)
	}))
	defer server.Close()
	client, err := moysklad.New(moysklad.Options{BaseURL: server.URL + "/", Token: credential, Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := &connections{moyAccount: "synthetic-account", moy: client, checks: map[string]map[string]connectionCheck{}}
	r := httptest.NewRequest("POST", "http://localhost/probe", strings.NewReader(`{}`))
	r.SetPathValue("provider", "moysklad")
	r = r.WithContext(context.WithValue(r.Context(), auditContextKey{}, "session"))
	w := httptest.NewRecorder()
	c.probe(w, r)
	if strings.Contains(w.Body.String(), credential) || strings.Contains(logs.String(), credential) {
		t.Fatal("probe exposed authentication secret")
	}
}
func TestConnectionProbesAreBoundedSanitizedAndRejectMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, provider, body string
		status               int
		want                 string
	}{
		{"moy-empty", "moysklad", `{"meta":{"size":0},"rows":[]}`, 200, "VERIFIED"},
		{"moy-mismatch", "moysklad", `{"meta":{"size":1},"rows":[{"id":"p","accountId":"another"}]}`, 200, "ACCOUNT_IDENTITY_MISMATCH"},
		{"moy-auth", "moysklad", `{}`, 401, "INVALID_CREDENTIALS"},
		{"ozon-ok", "ozon", `{"result":{"items":[]}}`, 200, "VERIFIED"},
		{"ozon-permission", "ozon", `{}`, 403, "PERMISSION_DENIED"},
		{"ozon-malformed", "ozon", `{}`, 200, "PROBE_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tc.provider == "moysklad" && (r.Method != "GET" || r.URL.Path != "/entity/product" || r.URL.Query().Get("limit") != "1") {
					t.Error("unexpected MoySklad operation")
				}
				if tc.provider == "ozon" && (r.Method != "POST" || r.URL.Path != "/v3/product/list") {
					t.Error("unexpected Ozon operation")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			c := &connections{moyAccount: "selected", ozonAccount: "synthetic-client", checks: map[string]map[string]connectionCheck{}}
			if tc.provider == "moysklad" {
				c.moy, _ = moysklad.New(moysklad.Options{BaseURL: server.URL + "/", Token: "sensitive-synthetic-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
			} else {
				c.ozon, _ = ozon.New(ozon.Options{BaseURL: server.URL, ClientID: c.ozonAccount, APIKey: "sensitive-synthetic-key", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
			}
			r := httptest.NewRequest("POST", "http://localhost/probe", strings.NewReader(`{}`))
			r.SetPathValue("provider", tc.provider)
			r = r.WithContext(context.WithValue(r.Context(), auditContextKey{}, "session"))
			w := httptest.NewRecorder()
			c.probe(w, r)
			if calls.Load() != 1 || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("calls=%d response=%s", calls.Load(), w.Body.String())
			}
			if strings.Contains(w.Body.String(), "sensitive-synthetic") || strings.Contains(w.Body.String(), "synthetic-client") {
				t.Fatal("credential leaked")
			}
		})
	}
}
func TestCancelledProbeDoesNotSend(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	client, _ := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: "client", APIKey: "key", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	c := &connections{ozonAccount: "client", ozon: client, checks: map[string]map[string]connectionCheck{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "http://localhost/probe", strings.NewReader(`{}`))
	r.SetPathValue("provider", "ozon")
	r = r.WithContext(context.WithValue(ctx, auditContextKey{}, "session"))
	w := httptest.NewRecorder()
	c.probe(w, r)
	if calls.Load() != 0 || !strings.Contains(w.Body.String(), "CANCELLED") {
		t.Fatalf("cancelled probe sent a request: %s", w.Body.String())
	}
}

func TestProbeTimeoutStopsRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(500 * time.Millisecond):
		}
	}))
	defer server.Close()
	client, _ := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: "client", APIKey: "key", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	c := &connections{ozonAccount: "client", ozon: client, checks: map[string]map[string]connectionCheck{}}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("POST", "http://localhost/probe", strings.NewReader(`{}`))
	r.SetPathValue("provider", "ozon")
	r = r.WithContext(context.WithValue(ctx, auditContextKey{}, "session"))
	w := httptest.NewRecorder()
	c.probe(w, r)
	if calls.Load() != 1 || !strings.Contains(w.Body.String(), "TIMEOUT") {
		t.Fatal("timeout was not reported safely")
	}
}
func TestConnectionMutationRequiresOriginAndJSON(t *testing.T) {
	a, err := newAuth(strings.Repeat("x", 32), "http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	h := a.connectionMutation(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	for _, origin := range []string{"", "http://evil.invalid"} {
		r := httptest.NewRequest("POST", "http://localhost", strings.NewReader(`{}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != 403 {
			t.Fatal("invalid origin accepted")
		}
	}
}
