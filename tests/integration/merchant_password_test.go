//go:build integration

package integration

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/identity"
)

func TestMerchantPasswordChangeKeepsCurrentSessionAndRevokesOthers(t *testing.T) {
	db, _ := controlledFixture(t)
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	mailbox := &testMailbox{tokens: map[string]string{}}
	handler, err := console.MerchantHandler(db, "http://127.0.0.1:5173", mailbox)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	browser := newMerchantBrowser(t, server.URL)
	const email = "password-change@example.test"
	session := browser.registerAndLogin(t, mailbox, email)
	store, err := identity.New(db.Pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	user, err := store.Authenticate(ctx, email, "long-merchant-password")
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := store.CreateSession(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	change := func(current, next string, csrf bool) (int, string) {
		body, err := json.Marshal(map[string]string{"current_password": current, "new_password": next})
		if err != nil {
			t.Fatal(err)
		}
		status, raw := browser.request(t, http.MethodPost, "/api/profile/password", string(body), csrf)
		return status, string(raw)
	}
	if status, raw := change("wrong-current-password", "replacement-password", true); status != 400 || !strings.Contains(raw, "INVALID_CURRENT_PASSWORD") {
		t.Fatalf("wrong current password: %d %s", status, raw)
	}
	if status, raw := change("long-merchant-password", "short", true); status != 400 || !strings.Contains(raw, "INVALID_PASSWORD") {
		t.Fatalf("weak new password: %d %s", status, raw)
	}
	if status, _ := change("long-merchant-password", "replacement-password", false); status != 403 {
		t.Fatalf("password change without CSRF: %d", status)
	}
	if status, raw := change("long-merchant-password", "replacement-password", true); status != 204 {
		t.Fatalf("password change: %d %s", status, raw)
	}
	if status, _ := browser.request(t, http.MethodGet, "/api/session", "", false); status != 200 {
		t.Fatalf("current session was revoked: %d", status)
	}
	if _, err = store.Session(ctx, otherToken); !errors.Is(err, identity.ErrAuth) {
		t.Fatalf("other session survived password change: %v", err)
	}
	if _, err = store.Authenticate(ctx, email, "long-merchant-password"); !errors.Is(err, identity.ErrAuth) {
		t.Fatalf("old password remained valid: %v", err)
	}
	if _, err = store.Authenticate(ctx, email, "replacement-password"); err != nil {
		t.Fatalf("new password unavailable: %v", err)
	}
	var audits int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM saas_security_events
	 WHERE actor_user_id=$1 AND action='PASSWORD_CHANGED'`, session.User.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("password change audit count %d: %v", audits, err)
	}
}
