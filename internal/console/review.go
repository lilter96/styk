package console

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/styk/internal/postgres"
)

// reviewScope validates both trusted registrations before exposing either source.
func reviewScope(ctx context.Context, db *postgres.Store, tenant, moy, ozon string) (bool, error) {
	var valid bool
	err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$2) AND EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='ozon' AND account_id=$3)`, tenant, moy, ozon).Scan(&valid)
	return valid, err
}
func reviewParams(r *http.Request) (string, string, string, string, int, bool) {
	q := r.URL.Query()
	tenant, moy, ozon, search := strings.TrimSpace(q.Get("tenant")), strings.TrimSpace(q.Get("moysklad_account")), strings.TrimSpace(q.Get("ozon_account")), strings.TrimSpace(q.Get("search"))
	offset := 0
	var err error
	if q.Get("offset") != "" {
		offset, err = strconv.Atoi(q.Get("offset"))
	}
	return tenant, moy, ozon, search, offset, err == nil && tenant != "" && moy != "" && ozon != "" && len(tenant) <= 256 && len(moy) <= 256 && len(ozon) <= 256 && len(search) <= 100 && offset >= 0 && offset <= 100000
}
func searchTargets(db *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, moy, ozon, search, offset, ok := reviewParams(r)
		if !ok {
			writeError(w, 400, "INVALID_SCOPE")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), db.Timeout)
		defer cancel()
		valid, err := reviewScope(ctx, db, tenant, moy, ozon)
		if err != nil {
			writeError(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		if !valid {
			writeError(w, 404, "SCOPE_NOT_REGISTERED")
			return
		}
		rows, err := db.Pool.Query(ctx, `WITH latest AS (SELECT i.id FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1) SELECT e.kind,e.external_id,e.article,e.code FROM source_entities e JOIN latest l ON l.id=e.import_id WHERE e.kind IN ('product','variant') AND ($3='' OR e.external_id ILIKE '%'||$3||'%' OR e.article ILIKE '%'||$3||'%' OR e.code ILIKE '%'||$3||'%') ORDER BY e.kind,e.external_id LIMIT 51 OFFSET $4`, tenant, moy, search, offset)
		if err != nil {
			writeError(w, 503, "SEARCH_UNAVAILABLE")
			return
		}
		defer rows.Close()
		type item struct {
			Kind    string `json:"kind"`
			ID      string `json:"id"`
			Article string `json:"article"`
			Code    string `json:"code"`
		}
		items := []item{}
		for rows.Next() {
			var v item
			if rows.Scan(&v.Kind, &v.ID, &v.Article, &v.Code) != nil {
				writeError(w, 503, "SEARCH_UNAVAILABLE")
				return
			}
			items = append(items, v)
		}
		if rows.Err() != nil {
			writeError(w, 503, "SEARCH_UNAVAILABLE")
			return
		}
		more := len(items) > 50
		if more {
			items = items[:50]
		}
		writeJSON(w, struct {
			Items   []item `json:"items"`
			HasNext bool   `json:"has_next"`
			Offset  int    `json:"offset"`
		}{items, more, offset})
	}
}
func mappingHistory(db *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, moy, ozon, _, offset, ok := reviewParams(r)
		product := strings.TrimSpace(r.URL.Query().Get("product"))
		if !ok || product == "" || len(product) > 256 {
			writeError(w, 400, "INVALID_SCOPE")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), db.Timeout)
		defer cancel()
		valid, err := reviewScope(ctx, db, tenant, moy, ozon)
		if err != nil {
			writeError(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		if !valid {
			writeError(w, 404, "SCOPE_NOT_REGISTERED")
			return
		}
		rows, err := db.Pool.Query(ctx, `SELECT h.mapping_id,h.version,h.ozon_product_id,h.ozon_offer_id,h.target_kind,h.target_id,h.provenance,h.confirmed_at,h.ozon_import_id,h.moysklad_import_id,a.review_id,a.evidence_class,a.principal_type,a.explanation FROM product_mapping_history h JOIN stock_connections mc ON mc.tenant_id=h.tenant_id AND mc.source='moysklad' AND mc.account_id=h.moysklad_account JOIN stock_connections oc ON oc.tenant_id=h.tenant_id AND oc.source='ozon' AND oc.account_id=h.ozon_account JOIN source_imports mi ON mi.id=h.moysklad_import_id AND mi.source='moysklad' AND mi.account_id=h.moysklad_account JOIN source_imports oi ON oi.id=h.ozon_import_id AND oi.source='ozon' AND oi.account_id=h.ozon_account LEFT JOIN product_mapping_confirmation_audits a ON a.mapping_id=h.mapping_id AND a.mapping_version=h.version AND a.tenant_id=h.tenant_id AND a.ozon_account=h.ozon_account AND a.moysklad_account=h.moysklad_account WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3 AND h.ozon_product_id=$4 AND (mc.include_import_history OR (mi.started_at>=mc.registered_at AND h.confirmed_at>=mc.registered_at)) AND (oc.include_import_history OR (oi.started_at>=oc.registered_at AND h.confirmed_at>=oc.registered_at)) ORDER BY h.mapping_id,h.version LIMIT 51 OFFSET $5`, tenant, moy, ozon, product, offset)
		if err != nil {
			writeError(w, 503, "HISTORY_UNAVAILABLE")
			return
		}
		defer rows.Close()
		type item struct {
			ID          int64     `json:"mapping_id"`
			Version     int       `json:"version"`
			Product     string    `json:"product_id"`
			Offer       string    `json:"offer_id"`
			Kind        string    `json:"target_kind"`
			Target      string    `json:"target_id"`
			Provenance  string    `json:"provenance"`
			Confirmed   time.Time `json:"confirmed_at"`
			OzonImport  int64     `json:"ozon_import_id"`
			MoyImport   int64     `json:"moysklad_import_id"`
			ReviewID    *string   `json:"review_id,omitempty"`
			Evidence    *string   `json:"evidence_class,omitempty"`
			Principal   *string   `json:"principal_type,omitempty"`
			Explanation *string   `json:"explanation,omitempty"`
		}
		items := []item{}
		for rows.Next() {
			var v item
			if rows.Scan(&v.ID, &v.Version, &v.Product, &v.Offer, &v.Kind, &v.Target, &v.Provenance, &v.Confirmed, &v.OzonImport, &v.MoyImport, &v.ReviewID, &v.Evidence, &v.Principal, &v.Explanation) != nil {
				writeError(w, 503, "HISTORY_UNAVAILABLE")
				return
			}
			items = append(items, v)
		}
		if rows.Err() != nil {
			writeError(w, 503, "HISTORY_UNAVAILABLE")
			return
		}
		more := len(items) > 50
		if more {
			items = items[:50]
		}
		writeJSON(w, struct {
			Items   []item `json:"items"`
			HasNext bool   `json:"has_next"`
			Offset  int    `json:"offset"`
		}{items, more, offset})
	}
}
