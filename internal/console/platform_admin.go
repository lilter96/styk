package console

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/postgres"
)

type platformAdmin struct{ db *postgres.Store }

var platformAdminIDPattern = regexp.MustCompile(`^u_[a-f0-9]{32}$`)

func configuredPlatformAdmin(raw string) string {
	id := strings.TrimSpace(raw)
	if !platformAdminIDPattern.MatchString(id) {
		return ""
	}
	return id
}

func registerPlatformAdmin(mux *http.ServeMux, auth *merchantAuth, db *postgres.Store) {
	if auth == nil {
		return
	}
	id := configuredPlatformAdmin(os.Getenv("STYK_PLATFORM_ADMIN_IDS"))
	auth.platformAdminID = id
	api := platformAdmin{db}
	mux.HandleFunc("GET /api/admin/overview", auth.withSession(platformAdminOnly(id, api.overview)))
	mux.HandleFunc("GET /api/admin/users", auth.withSession(platformAdminOnly(id, api.users)))
	mux.HandleFunc("POST /api/admin/support", auth.withSession(platformAdminOnly(id, auth.startSupport)))
	mux.HandleFunc("DELETE /api/admin/support", auth.withSession(platformAdminOnly(id, auth.endSupport)))
}

func platformAdminOnly(id string, next http.HandlerFunc) func(http.ResponseWriter, *http.Request, identity.Session) {
	return func(w http.ResponseWriter, r *http.Request, session identity.Session) {
		if id == "" || session.User.ID != id {
			writeError(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		if _, active := supportProof(r); active && r.Pattern != "DELETE /api/admin/support" {
			writeError(w, http.StatusConflict, "SUPPORT_ACTIVE")
			return
		}
		next(w, r)
	}
}

func (a platformAdmin) overview(w http.ResponseWriter, r *http.Request) {
	days, ok := adminInt(r.URL.Query().Get("days"), 30, 1, 90)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ADMIN_QUERY")
		return
	}
	a.query(w, r, adminOverviewSQL, days)
}

func (a platformAdmin) users(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, pageOK := adminInt(q.Get("page"), 1, 1, 100000)
	limit, limitOK := adminInt(q.Get("limit"), 20, 1, 50)
	search, status := strings.TrimSpace(q.Get("q")), q.Get("status")
	if !pageOK || !limitOK || len(search) > 256 || !slices.Contains([]string{"", "active", "unverified", "disabled"}, status) {
		writeError(w, http.StatusBadRequest, "INVALID_ADMIN_QUERY")
		return
	}
	a.query(w, r, adminUsersSQL, search, status, limit, (page-1)*limit)
}

func adminInt(raw string, fallback, min, max int) (int, bool) {
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil && n >= min && n <= max
}

func (a platformAdmin) query(w http.ResponseWriter, r *http.Request, query string, args ...any) {
	ctx, cancel := context.WithTimeout(r.Context(), a.db.Timeout)
	defer cancel()
	var body json.RawMessage
	if err := a.db.Pool.QueryRow(ctx, query, args...).Scan(&body); err != nil {
		writeError(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE")
		return
	}
	writeJSON(w, body)
}
