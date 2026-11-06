package postgres

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

type OrderWarehouseRow struct {
	OzonID    string `json:"ozon_id"`
	OzonName  string `json:"ozon_name"`
	StoreID   string `json:"store_id"`
	StoreName string `json:"store_name"`
	Status    string `json:"status"`
}

type OrderWarehousePage struct {
	Scope      orders.Scope        `json:"scope"`
	Items      []OrderWarehouseRow `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
	Confirmed  int                 `json:"confirmed"`
	Total      int                 `json:"total"`
}

// OrderWarehouses reads only canonical local import and mapping evidence. It
// performs no provider requests, and paginates in one consistent scope snapshot.
func (s *Store) OrderWarehouses(ctx context.Context, scope orders.Scope, page orders.PageRequest) (OrderWarehousePage, error) {
	result := OrderWarehousePage{Scope: scope, Items: []OrderWarehouseRow{}}
	if scope.Validate() != nil || page.Limit < 1 || page.Limit > 100 || len(page.Cursor) > 256 {
		return result, orders.ErrInvalid
	}
	result.Items = make([]OrderWarehouseRow, 0, page.Limit)
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, ordersError("begin order warehouse read", err)
	}
	defer rollbackMerchantMutation(tx)
	if _, err = readOrderConfig(ctx, tx, scope); err != nil {
		return result, err
	}
	mappings, err := readOrderWarehouseMappings(ctx, tx, scope)
	if err != nil {
		return result, err
	}
	result.Confirmed = len(mappings)
	rows, err := tx.Query(ctx, orderWarehousePageSQL, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, page.Cursor, page.Limit+1)
	if err != nil {
		return result, ordersError("read order warehouse page", err)
	}
	result, err = scanOrderWarehousePage(rows, result, mappings, page.Limit)
	if err != nil {
		return result, err
	}
	return result, ordersRowsError("commit order warehouse read", tx.Commit(ctx))
}

func scanOrderWarehousePage(rows pgx.Rows, result OrderWarehousePage, mappings []orders.WarehouseMappingRef, limit int) (OrderWarehousePage, error) {
	defer rows.Close()
	valid := make(map[string]orders.WarehouseMappingRef, len(mappings))
	for _, mapping := range mappings {
		valid[mapping.ID] = mapping
	}
	for rows.Next() {
		var row OrderWarehouseRow
		var id int64
		if err := rows.Scan(&row.OzonID, &row.OzonName, &row.StoreID, &row.StoreName, &id, &result.Total); err != nil {
			return result, ordersError("scan order warehouse page", err)
		}
		row.Status = "missing"
		if id != 0 {
			row.Status = "unconfirmed"
			if _, found := valid[strconv.FormatInt(id, 10)]; found {
				row.Status = "confirmed"
			}
		}
		result.Items = append(result.Items, row)
	}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.NextCursor = result.Items[limit-1].OzonID
	}
	return result, ordersRowsError("read order warehouse rows", rows.Err())
}

const orderWarehousePageSQL = `WITH oi AS (
 SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed'
 JOIN stock_connections c ON c.tenant_id=$1 AND c.source='ozon' AND c.account_id=i.account_id
 WHERE i.source='ozon' AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1
), mi AS (
 SELECT i.id FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source='moysklad' AND c.account_id=i.account_id
 WHERE i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1
), ow AS (SELECT w.* FROM ozon_warehouse_observations w JOIN oi ON oi.id=w.import_id),
 mw AS (SELECT e.* FROM source_entities e JOIN mi ON mi.id=e.import_id WHERE e.kind='store'),
 m AS (SELECT * FROM warehouse_mappings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND status='active'),
 ids AS (SELECT warehouse_id AS id FROM ow UNION SELECT ozon_warehouse_id FROM m),
 all_rows AS (SELECT ids.id,COALESCE(ow.name,'') AS ozon_name,COALESCE(m.moysklad_warehouse_id,'') AS store_id,COALESCE(mw.name,'') AS store_name,COALESCE(m.id,0) AS mapping_id FROM ids LEFT JOIN ow ON ow.warehouse_id=ids.id LEFT JOIN m ON m.ozon_warehouse_id=ids.id LEFT JOIN mw ON mw.external_id=m.moysklad_warehouse_id)
 SELECT *, (SELECT count(*) FROM all_rows) FROM all_rows WHERE id>$4 ORDER BY id LIMIT $5`
