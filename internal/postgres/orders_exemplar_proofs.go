package postgres

import (
	"context"
	"github.com/lilter96/styk/internal/orders"
)

func enrichOrderExemplarProofs(ctx context.Context, q ordersQuerier, result *orders.ReviewContext) error {
	result.ExemplarProofs = make(map[string]orders.ExemplarEvidence)
	numbers := make([]string, 0, len(result.Postings))
	for _, p := range result.Postings {
		numbers = append(numbers, p.PostingNumber)
	}
	if len(numbers) == 0 {
		return nil
	}
	scope := result.Config.Scope
	rows, err := q.Query(ctx, `SELECT DISTINCT ON(w.posting_number) w.posting_number,w.work#>'{receipt,exemplar_evidence}' FROM orders_work w JOIN orders_exemplar_secrets e ON e.work_id=w.id WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=ANY($4) AND w.action IN ('posting_exemplar_set','posting_exemplar_prepare') AND w.work#>>'{receipt,exemplar_evidence,packing_ready}'='true' AND w.state='RECONCILED' AND e.expires_at>clock_timestamp() AND e.credential_revision=$5 ORDER BY w.posting_number,w.finished_at DESC,w.id DESC`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, numbers, result.Credentials.Ozon)
	if err != nil {
		return ordersError("read exact-scope exemplar packing proofs", err)
	}
	defer rows.Close()
	for rows.Next() {
		var number string
		var raw []byte
		if err = rows.Scan(&number, &raw); err != nil {
			return ordersError("scan exemplar packing proof", err)
		}
		if len(raw) == 0 {
			continue
		}
		var proof orders.ExemplarEvidence
		if err = ordersDecode(raw, &proof); err != nil {
			return err
		}
		result.ExemplarProofs[number] = proof
	}
	return ordersRowsError("iterate exemplar packing proofs", rows.Err())
}
