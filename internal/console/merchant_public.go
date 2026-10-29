package console

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/identity"
)

func (m *merchantAuth) publicRequest(w http.ResponseWriter, r *http.Request, out any, bucket string) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if !m.safe(r) || !m.sameOrigin(r) || err != nil || media != "application/json" {
		writeError(w, http.StatusForbidden, "FORBIDDEN")
		return false
	}
	peer, err := m.clientAddress(r)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN")
		return false
	}
	if !m.allowIdentity(w, r, bucket+":peer:"+peer, 30) {
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return false
	}
	return true
}

func (m *merchantAuth) allowIdentity(w http.ResponseWriter, r *http.Request, key string, limit int) bool {
	hash := sha256.Sum256([]byte(key))
	allowed, err := m.identity.AllowAuthAttempt(r.Context(), hash[:], limit, 5*time.Minute)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return false
	}
	if !allowed {
		w.Header().Set("Retry-After", "300")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED")
		return false
	}
	return true
}

func (m *merchantAuth) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		// Older clients sent this field; only the verified mailbox holder sets
		// the usable password, so its value is intentionally ignored.
		Password  string `json:"password"`
		Workspace string `json:"workspace"`
	}
	if !m.publicRequest(w, r, &body, "register") || !m.allowIdentity(w, r, "register:email:"+strings.ToLower(strings.TrimSpace(body.Email)), 10) {
		return
	}
	// Older clients may still send a workspace name. The merchant account is
	// created automatically and its internal label is not user configuration.
	_, _, err := m.identity.RegisterPending(r.Context(), body.Email, "Магазин")
	if errors.Is(err, identity.ErrInvalid) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	if err != nil && !errors.Is(err, identity.ErrConflict) {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	m.deliverVerification(r, body.Email)
	w.WriteHeader(http.StatusAccepted)
}

func (m *merchantAuth) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !m.publicRequest(w, r, &body, "login") || !m.allowIdentity(w, r, "login:email:"+strings.ToLower(strings.TrimSpace(body.Email)), 10) {
		return
	}
	user, err := m.identity.Authenticate(r.Context(), body.Email, body.Password)
	if errors.Is(err, identity.ErrAuth) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	session, token, err := m.identity.CreateSession(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: merchantCookie, Value: token, Path: "/", HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteStrictMode, Expires: session.Expires, MaxAge: 8 * 60 * 60})
	w.WriteHeader(http.StatusNoContent)
}

func (m *merchantAuth) deliverVerification(r *http.Request, email string) {
	address, token, err := m.identity.CreateVerification(r.Context(), email)
	if err != nil {
		slog.Error("identity verification intent unavailable")
		return
	}
	if token != "" && m.sender.Send(r.Context(), address, "VERIFY_EMAIL", m.origin+"/#verify="+token) != nil {
		slog.Error("identity verification delivery unavailable")
	}
}

func (m *merchantAuth) resendVerification(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !m.publicRequest(w, r, &body, "verify-request") || !m.allowIdentity(w, r, "verify:email:"+strings.ToLower(strings.TrimSpace(body.Email)), 10) {
		return
	}
	m.deliverVerification(r, body.Email)
	w.WriteHeader(http.StatusAccepted)
}

func (m *merchantAuth) verify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !m.publicRequest(w, r, &body, "verify") {
		return
	}
	err := m.identity.VerifyEmail(r.Context(), body.Token, body.Password)
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
