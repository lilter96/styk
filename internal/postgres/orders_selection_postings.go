package postgres

import (
	"context"
	"fmt"

	"github.com/lilter96/styk/internal/orders"
)

func readOrderSelectionPostings(ctx context.Context, q ordersQuerier, config orders.Config, selection orderReviewSelection) ([]orders.Posting, error) {
	if selection.exact {
		return readExactOrderPostings(ctx, q, config.Scope, selection.numbers)
	}
	after := ""
	if selection.automatic {
		var err error
		after, err = orderAutomaticReviewCursor(ctx, q, config)
		if err != nil {
			return nil, err
		}
	}
	postings, err := readOrderPostings(ctx, q, config.Scope, after, "", orders.MaxPagePostings)
	if err != nil || len(postings) > 0 || after == "" {
		return postings, err
	}
	return readOrderPostings(ctx, q, config.Scope, "", "", orders.MaxPagePostings)
}

func readExactOrderPostings(ctx context.Context, q ordersQuerier, scope orders.Scope, numbers []string) ([]orders.Posting, error) {
	if len(numbers) == 0 {
		return []orders.Posting{}, nil
	}
	args := append(ordersScopeArgs(scope), numbers)
	rows, err := q.Query(ctx, `SELECT h.projection,h.checked_at,h.quarantined FROM orders_posting_heads h JOIN orders_posting_observations o ON o.id=h.observation_id AND o.tenant_id=h.tenant_id AND o.moysklad_account=h.moysklad_account AND o.ozon_account=h.ozon_account AND o.posting_number=h.posting_number WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3 AND h.posting_number=ANY($4::text[]) ORDER BY h.posting_number LIMIT 100`, args...)
	if err != nil {
		return nil, ordersError("select reviewed order postings", err)
	}
	defer rows.Close()
	postings := make([]orders.Posting, 0, len(numbers))
	for rows.Next() {
		posting, scanErr := scanOrderPosting(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		postings = append(postings, posting)
	}
	if err = rows.Err(); err != nil {
		return nil, ordersError("read reviewed order postings", err)
	}
	if len(postings) != len(numbers) {
		return nil, orders.ErrNotFound
	}
	return postings, nil
}

func orderAutomaticReviewCursor(ctx context.Context, q ordersQuerier, config orders.Config) (string, error) {
	if config.Automation == nil {
		return "", nil
	}
	actor := fmt.Sprintf("AUTOMATION:%s:%d", config.Automation.ID, config.Automation.Revision)
	args := append(ordersScopeArgs(config.Scope), config.Revision, actor)
	var after string
	err := q.QueryRow(ctx, `SELECT COALESCE(max(decision->'posting'->>'posting_number'),'') FROM (SELECT preview FROM orders_previews WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND config_revision=$4 AND automatic AND actor=$5 ORDER BY created_at DESC,id DESC LIMIT 1) latest CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(latest.preview->'decisions')='array' THEN latest.preview->'decisions' ELSE '[]'::jsonb END) decision`, args...).Scan(&after)
	return after, ordersRowsError("read durable automatic order review cursor", err)
}
