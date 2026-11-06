package console

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/postgres"
)

type warehouseStockQuery struct {
	tenant, source, account, warehouse, search string
	offset                                     int
}

func parseWarehouseStockQuery(r *http.Request) (warehouseStockQuery, string) {
	q := r.URL.Query()
	input := warehouseStockQuery{tenant: strings.TrimSpace(q.Get("tenant")), source: q.Get("source"), account: strings.TrimSpace(q.Get("account")), warehouse: strings.TrimSpace(q.Get("warehouse")), search: strings.TrimSpace(q.Get("search"))}
	for _, v := range []string{input.tenant, input.account, input.warehouse} {
		if v == "" || len(v) > 256 {
			return input, "INVALID_SCOPE"
		}
	}
	if input.source != "moysklad" && input.source != "ozon" {
		return input, "INVALID_SCOPE"
	}
	if q.Has("offset") {
		var err error
		input.offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil || input.offset < 0 || input.offset > 100000 {
			return input, "INVALID_PAGE"
		}
	}
	if len(input.search) > 256 {
		return input, "INVALID_SEARCH"
	}
	return input, ""
}

// stock returns source-reported fields separately and never fills missing rows.
func stock(db *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		input, code := parseWarehouseStockQuery(r)
		if code != "" {
			writeError(w, 400, code)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), db.Timeout)
		defer cancel()
		var valid bool
		err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source=$2 AND account_id=$3)`, input.tenant, input.source, input.account).Scan(&valid)
		if err != nil {
			writeError(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		if !valid {
			writeError(w, 404, "SCOPE_NOT_REGISTERED")
			return
		}
		result, err := db.WarehouseStock(ctx, input.tenant, input.source, input.account, input.warehouse, input.search, input.offset)
		if err != nil {
			writeError(w, 503, "STOCK_UNAVAILABLE")
			return
		}
		writeJSON(w, result)
	}
}
