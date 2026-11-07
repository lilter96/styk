package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func enrichMerchantPending(ctx context.Context, q ordersQuerier, review *orders.ReviewContext, numbers []string) error {
	review.MerchantPending = make(map[string]bool)
	if review.Config.Lifecycle == nil || !review.Config.Lifecycle.Enabled || len(numbers) == 0 {
		return nil
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT posting_number FROM orders_merchant_intents WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND config_revision=$4 AND posting_number=ANY($5::text[]) AND state='OBSERVED' AND (intent->>'observed_at')::timestamptz>clock_timestamp()-interval '15 minutes'`, append(ordersScopeArgs(review.Config.Scope), review.Config.Revision, numbers)...)
	if err != nil {
		return ordersError("read pending merchant transition guards", err)
	}
	defer rows.Close()
	for rows.Next() {
		var number string
		if err = rows.Scan(&number); err != nil {
			return ordersError("scan pending merchant guard", err)
		}
		review.MerchantPending[number] = true
	}
	return ordersRowsError("read pending merchant transition guards", rows.Err())
}
