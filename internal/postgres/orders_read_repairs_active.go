package postgres

import (
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// Both indexed branches are keyset bounded before UNION; no historical JSON
// pages are scanned for repair. A failed first-ever identity is a READ KEY,
// never an invented posting observation, customer or inventory quantity.
const activeOrderRepairKeysSQL = `WITH accepted AS (
 SELECT h.posting_number FROM orders_posting_heads h JOIN orders_posting_observations o
 ON o.id=h.observation_id AND o.tenant_id=h.tenant_id AND o.moysklad_account=h.moysklad_account
 AND o.ozon_account=h.ozon_account AND o.posting_number=h.posting_number
 WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3
 AND h.posting_number>$4 AND h.projection#>>'{scheme,value}' IS DISTINCT FROM 'FBO' AND o.posting#>>'{scheme,value}' IS DISTINCT FROM 'FBO'
 AND ((h.active AND NOT COALESCE(o.posting#>>'{status,presence}'='value' AND o.posting#>>'{status,value}' IN ('delivered','cancelled'),false))
 OR (o.posting#>>'{status,presence}'='value' AND o.posting#>>'{status,value}' IN ('delivered','cancelled')
 AND h.checked_at<=clock_timestamp()-interval '6 hours'
 AND o.observed_at>=clock_timestamp()-interval '31 days' AND o.observed_at<=clock_timestamp()+interval '1 minute'))
 ORDER BY h.posting_number LIMIT $5
), repair AS (
 SELECT r.posting_number FROM orders_read_repairs r
 WHERE r.tenant_id=$1 AND r.moysklad_account=$2 AND r.ozon_account=$3
 AND r.resolved_at IS NULL AND r.posting_number>$4
 AND NOT EXISTS(SELECT 1 FROM orders_posting_heads h JOIN orders_posting_observations o ON o.id=h.observation_id
 WHERE h.tenant_id=r.tenant_id AND h.moysklad_account=r.moysklad_account AND h.ozon_account=r.ozon_account
 AND h.posting_number=r.posting_number AND (h.projection#>>'{scheme,value}'='FBO' OR o.posting#>>'{scheme,value}'='FBO'))
 ORDER BY r.posting_number LIMIT $5
), keys AS (SELECT posting_number FROM accepted UNION SELECT posting_number FROM repair)
SELECT k.posting_number,h.projection,h.checked_at,COALESCE(h.quarantined,false)
FROM keys k LEFT JOIN orders_posting_heads h ON h.tenant_id=$1 AND h.moysklad_account=$2
 AND h.ozon_account=$3 AND h.posting_number=k.posting_number ORDER BY k.posting_number LIMIT $5`

func scanActiveOrderReadKey(row pgx.Row, scope orders.Scope) (orders.Posting, error) {
	var number string
	var raw []byte
	var checked *time.Time
	var quarantined bool
	if err := row.Scan(&number, &raw, &checked, &quarantined); err != nil {
		return orders.Posting{}, ordersError("read active posting key", err)
	}
	if len(raw) == 0 {
		return orders.Posting{Scope: scope, PostingNumber: number}, nil
	}
	var posting orders.Posting
	if err := ordersDecode(raw, &posting); err != nil {
		return posting, err
	}
	if checked != nil {
		posting.Evidence.CheckedAt = *checked
	}
	if quarantined {
		posting.Drift = append(posting.Drift, orders.Drift{Path: "evidence", Code: "ORDERING_SUPERSEDED_OBSERVATION"})
	}
	return posting, nil
}
