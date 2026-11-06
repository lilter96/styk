package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/identity"
)

const supportCookie = "styk_support"

type supportSessionView struct {
	Workspace string     `json:"workspace,omitempty"`
	Expires   *time.Time `json:"expires_at,omitempty"`
	Expired   bool       `json:"expired"`
}

func supportProof(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(supportCookie)
	if err != nil {
		return "", false
	}
	return cookie.Value, true
}

func (m *merchantAuth) startSupport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tenant string `json:"tenant_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || body.Tenant != strings.TrimSpace(body.Tenant) {
		writeError(w, http.StatusBadRequest, "INVALID_SUPPORT_TARGET")
		return
	}
	admin := sessionFrom(r)
	proof, err := m.identity.StartSupport(r.Context(), admin, body.Tenant)
	switch {
	case errors.Is(err, identity.ErrInvalid):
		writeError(w, http.StatusBadRequest, "INVALID_SUPPORT_TARGET")
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusNotFound, "SUPPORT_TARGET_UNAVAILABLE")
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
	default:
		http.SetCookie(w, &http.Cookie{Name: supportCookie, Value: proof, Path: "/", HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteStrictMode, Expires: admin.Expires})
		w.WriteHeader(http.StatusNoContent)
	}
}

func (m *merchantAuth) clearSupport(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: supportCookie, Path: "/", HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func (m *merchantAuth) endSupport(w http.ResponseWriter, r *http.Request) {
	if proof, active := supportProof(r); active {
		if err := m.identity.EndSupport(r.Context(), sessionFrom(r), proof); err != nil {
			writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
			return
		}
	}
	m.clearSupport(w)
	w.WriteHeader(http.StatusNoContent)
}

func supportReadAllowed(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	switch r.Pattern {
	case "GET /api/session", "GET /api/registrations", "GET /api/integrations",
		"GET /api/diagnostics", "GET /api/imports", "GET /api/imports/{id}",
		"GET /api/products", "GET /api/moysklad-products", "GET /api/product-mapping-history",
		"GET /api/warehouses", "GET /api/stock", "GET /api/orders/config",
		"GET /api/orders/postings", "GET /api/orders/operations":
		return true
	default:
		return false
	}
}

func (m *merchantAuth) serveSupport(w http.ResponseWriter, r *http.Request, admin identity.Session, proof string, next http.HandlerFunc) {
	if !supportReadAllowed(r) {
		writeError(w, http.StatusForbidden, "SUPPORT_READ_ONLY")
		return
	}
	if m.platformAdminID == "" || admin.User.ID != m.platformAdminID {
		if r.Pattern == "GET /api/session" {
			m.clearSupport(w)
			m.session(w, r)
			return
		}
		writeError(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	access, err := m.identity.Support(r.Context(), admin, proof)
	if err != nil {
		if !errors.Is(err, identity.ErrAuth) {
			writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		} else if r.Pattern == "GET /api/session" {
			writeMerchantSession(w, admin, "", &supportSessionView{Expired: true})
		} else {
			writeError(w, http.StatusForbidden, "SUPPORT_EXPIRED")
		}
		return
	}
	effective := admin
	effective.User, effective.Expires = access.User, access.Expires
	if r.Pattern == "GET /api/session" {
		expires := access.Expires.UTC()
		writeMerchantSession(w, effective, access.Workspace.ID, &supportSessionView{Workspace: access.Workspace.Name, Expires: &expires})
		return
	}
	if err := m.scopeRequest(r, effective, access.Workspace.ID); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), access.Expires)
	defer cancel()
	ctx = context.WithValue(ctx, merchantSessionKey{}, effective)
	next(w, r.WithContext(identity.WithSession(ctx, effective)))
}
