//go:build integration

package integration

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"testing"
)

// Only the disposable process harness exposes this mailbox, never the API.
// Its secret is separate from the child-process control secret.
func ordersProcessBrowserMail(t *testing.T, mailbox *testMailbox, secret string, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	w.Header().Set("Cache-Control", "no-store")
	if secret == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Test-Secret")), []byte(secret)) != 1 {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet || r.URL.Query().Get("email") != "orders-browser-register@example.test" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"token": mailbox.token("orders-browser-register@example.test", "VERIFY_EMAIL")}); err != nil {
		t.Error("write synthetic browser mailbox response")
	}
}
