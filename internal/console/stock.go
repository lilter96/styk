package console

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/postgres"
)

// stock returns source-reported fields separately and never fills missing rows.
func stock(db *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tenant := strings.TrimSpace(q.Get("tenant"))
		source := q.Get("source")
		account := strings.TrimSpace(q.Get("account"))
		warehouse := strings.TrimSpace(q.Get("warehouse"))
		if tenant == "" || account == "" || warehouse == "" || len(tenant) > 256 || len(account) > 256 || len(warehouse) > 256 || (source != "moysklad" && source != "ozon") {
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
		sql := `WITH latest AS (SELECT i.id FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source=$2 AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1) SELECT s.kind,s.external_id,s.stock::text,s.reserve::text,s.in_transit::text,s.quantity::text,NULL::text,NULL::text,NULL::text,s.observed_at FROM source_warehouse_stock_observations s JOIN latest l ON l.id=s.import_id WHERE s.warehouse_id=$4 ORDER BY s.kind,s.external_id LIMIT 51 OFFSET $5`
		if source == "ozon" {
			sql = `WITH latest AS (SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source=$2 AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1) SELECT 'product'::text,s.product_id,NULL::text,NULL::text,NULL::text,NULL::text,s.present::text,s.reserved::text,s.free_stock::text,s.observed_at FROM ozon_fbs_stock_observations s JOIN latest l ON l.id=s.import_id WHERE s.warehouse_id=$4 ORDER BY s.product_id LIMIT 51 OFFSET $5`
		}
		rows, e := db.Pool.Query(ctx, sql, tenant, source, account, warehouse, offset)
		if e != nil {
			writeError(w, 503, "STOCK_UNAVAILABLE")
			return
		}
		defer rows.Close()
		type item struct {
			Kind       string    `json:"kind"`
			ItemID     string    `json:"item_id"`
			Stock      *string   `json:"stock"`
			Reserve    *string   `json:"reserve"`
			Incoming   *string   `json:"incoming"`
			Quantity   *string   `json:"quantity"`
			Present    *string   `json:"present"`
			Reserved   *string   `json:"reserved"`
			FreeStock  *string   `json:"free_stock"`
			ObservedAt time.Time `json:"observed_at"`
		}
		items := []item{}
		for rows.Next() {
			var v item
			if rows.Scan(&v.Kind, &v.ItemID, &v.Stock, &v.Reserve, &v.Incoming, &v.Quantity, &v.Present, &v.Reserved, &v.FreeStock, &v.ObservedAt) != nil {
				writeError(w, 503, "STOCK_UNAVAILABLE")
				return
			}
			items = append(items, v)
		}
		if rows.Err() != nil {
			writeError(w, 503, "STOCK_UNAVAILABLE")
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
