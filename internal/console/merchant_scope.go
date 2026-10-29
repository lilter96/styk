package console

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/identity"
)

const merchantBodyLimit = 512 << 10

func (m *merchantAuth) authorizeScope(w http.ResponseWriter, r *http.Request, session identity.Session) bool {
	if r.URL.Path == "/api/session" || r.URL.Path == "/api/workspaces" {
		return true
	}
	workspace, err := m.requestWorkspace(r, session)
	if err != nil || workspace == "" {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
		return false
	}
	role, err := m.identity.Role(r.Context(), session.User.ID, workspace)
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
		return false
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return false
	}
	if !rolePermits(role, r) {
		writeError(w, http.StatusForbidden, "FORBIDDEN")
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if err = m.identity.RecordAction(r.Context(), session, workspace, r.Pattern); err != nil {
			writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
			return false
		}
	}
	return true
}

func rolePermits(role string, r *http.Request) bool {
	if strings.HasSuffix(r.Pattern, "/members") || strings.HasSuffix(r.Pattern, "/audit") {
		return identity.CanAdmin(role)
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return identity.CanRead(role)
	}
	if strings.HasPrefix(r.URL.Path, "/api/integrations/") || r.URL.Path == "/api/stock-sync/automatic" || r.URL.Path == "/api/stock-sync/enable" || r.URL.Path == "/api/stock-sync/disable" || strings.HasSuffix(r.URL.Path, "/supersede") {
		return identity.CanAdmin(role)
	}
	return identity.CanOperate(role)
}

func (m *merchantAuth) requestWorkspace(r *http.Request, session identity.Session) (string, error) {
	if workspace := r.PathValue("workspace"); workspace != "" {
		return workspace, nil
	}
	if r.PathValue("id") != "" && (strings.HasPrefix(r.URL.Path, "/api/product-mapping-reviews/") || strings.HasPrefix(r.URL.Path, "/api/warehouse-mapping-reviews/")) {
		return m.reviewWorkspace(r, session)
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", err
	}
	values := query["tenant"]
	if len(values) > 1 {
		return "", identity.ErrInvalid
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if len(values) != 1 {
			return "", identity.ErrInvalid
		}
		return values[0], nil
	}
	workspace, err := bodyWorkspace(r)
	if err != nil || (len(values) == 1 && values[0] != workspace) {
		return "", identity.ErrInvalid
	}
	return workspace, nil
}

func (m *merchantAuth) reviewWorkspace(r *http.Request, session identity.Session) (string, error) {
	id := r.PathValue("id")
	if len(id) != 32 {
		return "", identity.ErrInvalid
	}
	query := `SELECT tenant_id FROM product_mapping_reviews WHERE id=$1 AND session_audit_id=$2`
	if strings.HasPrefix(r.URL.Path, "/api/warehouse-mapping-reviews/") {
		query = `SELECT tenant_id FROM warehouse_mapping_reviews WHERE id=$1 AND session_audit_id=$2`
	}
	var workspace string
	err := m.db.Pool.QueryRow(r.Context(), query, id, session.AuditID).Scan(&workspace)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", identity.ErrForbidden
	}
	return workspace, err
}

func bodyWorkspace(r *http.Request) (string, error) {
	original := r.Body
	body, err := io.ReadAll(io.LimitReader(original, merchantBodyLimit+1))
	closeErr := original.Close()
	if err != nil || closeErr != nil || len(body) > merchantBodyLimit {
		return "", identity.ErrInvalid
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", identity.ErrInvalid
	}
	workspace, found := "", false
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return "", identity.ErrInvalid
		}
		key, ok := name.(string)
		if !ok {
			return "", identity.ErrInvalid
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return "", identity.ErrInvalid
		}
		if strings.EqualFold(key, "tenant") {
			if key != "tenant" || found || json.Unmarshal(value, &workspace) != nil {
				return "", identity.ErrInvalid
			}
			found = true
		}
	}
	if _, err = decoder.Token(); err != nil {
		return "", identity.ErrInvalid
	}
	if decoder.Decode(&struct{}{}) != io.EOF || !found || workspace == "" {
		return "", identity.ErrInvalid
	}
	return workspace, nil
}
