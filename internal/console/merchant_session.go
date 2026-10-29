package console

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/lilter96/styk/internal/identity"
)

func (m *merchantAuth) session(w http.ResponseWriter, r *http.Request) {
	session := sessionFrom(r)
	tenant, err := m.identity.MerchantTenant(r.Context(), session.User.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	writeJSON(w, struct {
		User      identity.User `json:"user"`
		AccountID string        `json:"account_id"`
		CSRF      string        `json:"csrf"`
		Expires   string        `json:"expires_at"`
	}{session.User, tenant, session.CSRF, session.Expires.UTC().Format("2006-01-02T15:04:05Z07:00")})
}

func (m *merchantAuth) logout(w http.ResponseWriter, r *http.Request) {
	if err := m.identity.RevokeSession(r.Context(), sessionFrom(r)); err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: merchantCookie, Value: "", Path: "/", HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (m *merchantAuth) requestReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !m.publicRequest(w, r, &body, "reset-request") || !m.allowIdentity(w, r, "reset:email:"+strings.ToLower(strings.TrimSpace(body.Email)), 10) {
		return
	}
	address, token, err := m.identity.CreateReset(r.Context(), body.Email)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	if token != "" && m.sender.Send(r.Context(), address, "RESET_PASSWORD", m.origin+"/#reset="+token) != nil {
		// A uniform response avoids disclosing whether the email is registered.
		// Operators receive a secret-free delivery failure signal.
		slog.Error("identity password recovery delivery unavailable")
	}
	w.WriteHeader(http.StatusAccepted)
}

func (m *merchantAuth) reset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !m.publicRequest(w, r, &body, "reset") {
		return
	}
	err := m.identity.ResetPassword(r.Context(), body.Token, body.Password)
	if errors.Is(err, identity.ErrAuth) || errors.Is(err, identity.ErrInvalid) {
		writeError(w, http.StatusBadRequest, "INVALID_OR_EXPIRED")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
