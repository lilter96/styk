package console

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/postgres"
)

func warehouses(db *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tenant := strings.TrimSpace(q.Get("tenant"))
		source := q.Get("source")
		account := strings.TrimSpace(q.Get("account"))
		other := strings.TrimSpace(q.Get("other_account"))
		if tenant == "" || account == "" || len(tenant) > 256 || len(account) > 256 || len(other) > 256 || (source != "moysklad" && source != "ozon") {
			writeError(w, 400, "INVALID_SCOPE")
			return
		}
		offset := 0
		if x := q.Get("offset"); x != "" {
			v, e := strconv.Atoi(x)
			if e != nil || v < 0 || v > 100000 {
				writeError(w, 400, "INVALID_PAGE")
				return
			}
			offset = v
		}
		ctx, cancel := context.WithTimeout(r.Context(), db.Timeout)
		defer cancel()
		var valid bool
		if e := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source=$2 AND account_id=$3)`, tenant, source, account).Scan(&valid); e != nil {
			writeError(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		if !valid {
			writeError(w, 404, "SCOPE_NOT_REGISTERED")
			return
		}
		if other != "" {
			otherSource := "ozon"
			if source == "ozon" {
				otherSource = "moysklad"
			}
			if e := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source=$2 AND account_id=$3)`, tenant, otherSource, other).Scan(&valid); e != nil {
				writeError(w, 503, "DATABASE_UNAVAILABLE")
				return
			}
			if !valid {
				writeError(w, 404, "SCOPE_NOT_REGISTERED")
				return
			}
		}
		sql := `WITH latest AS (SELECT i.id FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source=$2 AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1), stocks AS (SELECT s.warehouse_id,count(*) AS n FROM source_warehouse_stock_observations s JOIN latest l ON l.id=s.import_id GROUP BY s.warehouse_id) SELECT e.external_id,e.name,''::text,NULL::boolean,coalesce(s.n,0),CASE WHEN $5='' THEN NULL ELSE m.id IS NOT NULL END FROM source_entities e JOIN latest l ON l.id=e.import_id LEFT JOIN stocks s ON s.warehouse_id=e.external_id LEFT JOIN warehouse_mappings m ON m.tenant_id=$1 AND m.moysklad_account=$3 AND m.ozon_account=$5 AND m.moysklad_warehouse_id=e.external_id AND m.status='active' WHERE e.kind='store' ORDER BY e.external_id LIMIT 51 OFFSET $4`
		if source == "ozon" {
			sql = `WITH latest AS (SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source=$2 AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1), stocks AS (SELECT s.warehouse_id,count(*) AS n FROM ozon_fbs_stock_observations s JOIN latest l ON l.id=s.import_id GROUP BY s.warehouse_id) SELECT w.warehouse_id,w.name,w.status,w.is_rfbs,coalesce(s.n,0),CASE WHEN $5='' THEN NULL ELSE m.id IS NOT NULL END FROM ozon_warehouse_observations w JOIN latest l ON l.id=w.import_id LEFT JOIN stocks s ON s.warehouse_id=w.warehouse_id LEFT JOIN warehouse_mappings m ON m.tenant_id=$1 AND m.ozon_account=$3 AND m.moysklad_account=$5 AND m.ozon_warehouse_id=w.warehouse_id AND m.status='active' ORDER BY w.warehouse_id LIMIT 51 OFFSET $4`
		}
		rows, e := db.Pool.Query(ctx, sql, tenant, source, account, offset, other)
		if e != nil {
			writeError(w, 503, "WAREHOUSES_UNAVAILABLE")
			return
		}
		defer rows.Close()
		type item struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			IsRFBS    *bool  `json:"is_rfbs"`
			StockRows int64  `json:"stock_rows"`
			Mapped    *bool  `json:"mapped"`
		}
		items := []item{}
		for rows.Next() {
			var v item
			if rows.Scan(&v.ID, &v.Name, &v.Status, &v.IsRFBS, &v.StockRows, &v.Mapped) != nil {
				writeError(w, 503, "WAREHOUSES_UNAVAILABLE")
				return
			}
			items = append(items, v)
		}
		if rows.Err() != nil {
			writeError(w, 503, "WAREHOUSES_UNAVAILABLE")
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
