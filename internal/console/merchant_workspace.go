package console

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/lilter96/styk/internal/identity"
)

func (m *merchantAuth) session(w http.ResponseWriter, r *http.Request) {
	session := sessionFrom(r)
	workspaces, err := m.identity.Workspaces(r.Context(), session.User.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	writeJSON(w, struct {
		User       identity.User        `json:"user"`
		Workspaces []identity.Workspace `json:"workspaces"`
		CSRF       string               `json:"csrf"`
		Expires    string               `json:"expires_at"`
	}{session.User, workspaces, session.CSRF, session.Expires.UTC().Format("2006-01-02T15:04:05Z07:00")})
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

func (m *merchantAuth) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	items, err := m.identity.Workspaces(r.Context(), sessionFrom(r).User.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	writeJSON(w, struct {
		Workspaces []identity.Workspace `json:"workspaces"`
	}{items})
}

func (m *merchantAuth) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeIdentityBody(w, r, &body) {
		return
	}
	workspace, err := m.identity.CreateWorkspace(r.Context(), sessionFrom(r), body.Name)
	if errors.Is(err, identity.ErrInvalid) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, workspace)
}

func (m *merchantAuth) members(w http.ResponseWriter, r *http.Request) {
	items, err := m.identity.Members(r.Context(), sessionFrom(r), r.PathValue("workspace"))
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	writeJSON(w, struct {
		Members []identity.Member `json:"members"`
	}{items})
}

func (m *merchantAuth) workspaceAudit(w http.ResponseWriter, r *http.Request) {
	events, err := m.identity.WorkspaceEvents(r.Context(), sessionFrom(r), r.PathValue("workspace"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	writeJSON(w, struct {
		Events []identity.SecurityEvent `json:"events"`
	}{events})
}

func (m *merchantAuth) setMember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decodeIdentityBody(w, r, &body) {
		return
	}
	err := m.identity.SetMembership(r.Context(), sessionFrom(r), r.PathValue("workspace"), body.Email, body.Role)
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	if errors.Is(err, identity.ErrInvalid) || errors.Is(err, identity.ErrLastOwner) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeIdentityBody(w http.ResponseWriter, r *http.Request, out any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
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
