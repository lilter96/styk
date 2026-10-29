package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func readSelectedOrderCorrelations(ctx context.Context, q ordersQuerier, scope orders.Scope, numbers []string) ([]orders.Correlation, error) {
	result := make([]orders.Correlation, 0, len(numbers))
	if len(numbers) == 0 {
		return result, nil
	}
	args := append(ordersScopeArgs(scope), numbers)
	rows, err := q.Query(ctx, `SELECT posting_number,sync_id,marker,COALESCE(customer_order_id,'') FROM orders_projection_bindings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=ANY($4::text[]) ORDER BY posting_number LIMIT 100`, args...)
	if err != nil {
		return nil, ordersError("select bounded order correlations", err)
	}
	defer rows.Close()
	for rows.Next() {
		correlation := orders.Correlation{Scope: scope}
		if err = rows.Scan(&correlation.PostingNumber, &correlation.SyncID, &correlation.Marker, &correlation.CustomerOrderID); err != nil {
			return nil, ordersError("scan bounded order correlation", err)
		}
		result = append(result, correlation)
	}
	return result, ordersRowsError("read bounded order correlations", rows.Err())
}

func readSelectedOrderFences(ctx context.Context, q ordersQuerier, scope orders.Scope, numbers []string) (map[string]orders.State, error) {
	result := make(map[string]orders.State, len(numbers))
	if len(numbers) == 0 {
		return result, nil
	}
	args := append(ordersScopeArgs(scope), numbers)
	rows, err := q.Query(ctx, `SELECT posting_number,state FROM orders_work WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=ANY($4::text[]) AND state IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN') LIMIT 100`, args...)
	if err != nil {
		return nil, ordersError("select bounded order target fences", err)
	}
	defer rows.Close()
	for rows.Next() {
		var number string
		var state orders.State
		if err = rows.Scan(&number, &state); err != nil {
			return nil, ordersError("scan bounded order target fence", err)
		}
		result[number] = state
	}
	return result, ordersRowsError("read bounded order target fences", rows.Err())
}
