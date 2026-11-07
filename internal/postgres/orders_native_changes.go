package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) NativeOrderCorrelationsForIDs(ctx context.Context, scope orders.Scope, ids []string) ([]orders.Correlation, error) {
	if scope.Validate() != nil || len(ids) > orders.MaxPagePostings {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	rows, err := s.Pool.Query(ctx, `SELECT posting_number,sync_id,marker,customer_order_id FROM orders_projection_bindings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND customer_order_id=ANY($4::text[]) ORDER BY posting_number`, append(ordersScopeArgs(scope), ids)...)
	if err != nil {
		return nil, ordersError("resolve changed native order correlations", err)
	}
	defer rows.Close()
	result := make([]orders.Correlation, 0, len(ids))
	for rows.Next() {
		correlation := orders.Correlation{Scope: scope}
		if err = rows.Scan(&correlation.PostingNumber, &correlation.SyncID, &correlation.Marker, &correlation.CustomerOrderID); err != nil {
			return nil, ordersError("scan changed native correlation", err)
		}
		result = append(result, correlation)
	}
	return result, ordersRowsError("read changed native correlations", rows.Err())
}
