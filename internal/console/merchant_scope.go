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
	if r.URL.Path == "/api/session" {
		return true
	}
	tenant, err := m.identity.MerchantTenant(r.Context(), session.User.ID)
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
		return false
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return false
	}
	if err = m.scopeRequest(r, session, tenant); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if err = m.identity.RecordAction(r.Context(), session, tenant, r.Pattern); err != nil {
			writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
			return false
		}
	}
	return true
}

// Existing domain handlers receive their scoped input as before, but the
// authenticated user is the authority. A client-supplied scope is optional and
// may never select another account.
func (m *merchantAuth) scopeRequest(r *http.Request, session identity.Session, tenant string) error {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return identity.ErrInvalid
	}
	for key, values := range query {
		if strings.EqualFold(key, "tenant") && (key != "tenant" || len(values) != 1 || values[0] != tenant) {
			return identity.ErrForbidden
		}
	}
	query.Set("tenant", tenant)
	r.URL.RawQuery = query.Encode()
	if isMappingReviewObject(r) {
		owner, err := m.reviewWorkspace(r, session)
		if err != nil || owner != tenant {
			return identity.ErrForbidden
		}
		return nil
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return nil
	}
	return scopeMerchantBody(r, tenant)
}

func isMappingReviewObject(r *http.Request) bool {
	return r.PathValue("id") != "" && (strings.HasPrefix(r.URL.Path, "/api/product-mapping-reviews/") || strings.HasPrefix(r.URL.Path, "/api/warehouse-mapping-reviews/"))
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
	var tenant string
	err := m.db.Pool.QueryRow(r.Context(), query, id, session.AuditID).Scan(&tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", identity.ErrForbidden
	}
	return tenant, err
}

func scopeMerchantBody(r *http.Request, tenant string) error {
	original := r.Body
	body, err := io.ReadAll(io.LimitReader(original, merchantBodyLimit+1))
	closeErr := original.Close()
	if err != nil || closeErr != nil || len(body) > merchantBodyLimit {
		return identity.ErrInvalid
	}
	fields, err := merchantObject(body)
	if err != nil {
		return err
	}
	if value, found := fields["tenant"]; found {
		var supplied string
		if json.Unmarshal(value, &supplied) != nil || supplied != tenant {
			return identity.ErrForbidden
		}
	} else {
		value, err := json.Marshal(tenant)
		if err != nil {
			return identity.ErrInvalid
		}
		fields["tenant"] = value
	}
	body, err = json.Marshal(fields)
	if err != nil {
		return identity.ErrInvalid
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return nil
}

func merchantObject(body []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, identity.ErrInvalid
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		name, err := decoder.Token()
		key, ok := name.(string)
		if err != nil || !ok || strings.EqualFold(key, "tenant") && key != "tenant" {
			return nil, identity.ErrInvalid
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, identity.ErrInvalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, identity.ErrInvalid
		}
		fields[key] = value
	}
	if _, err = decoder.Token(); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, identity.ErrInvalid
	}
	return fields, nil
}
