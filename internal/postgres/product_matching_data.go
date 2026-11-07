package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

type productMatchSnapshot struct {
	MoyID, OzonID                   int64
	MoyFingerprint, OzonFingerprint string
	Total                           int
}

func productMatchingImports(ctx context.Context, tx pgx.Tx, scope catalog.Scope) (productMatchSnapshot, error) {
	data := productMatchSnapshot{}
	var fresh bool
	err := tx.QueryRow(ctx, `WITH imports AS (
 SELECT DISTINCT ON(i.source) i.source,i.id,i.fingerprint,i.finished_at,b.traversal_status
 FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
 LEFT JOIN ozon_import_batches b ON b.import_id=i.id
 WHERE ((i.source='moysklad' AND i.account_id=$2) OR (i.source='ozon' AND i.account_id=$3)) AND (c.include_import_history OR i.started_at>=c.registered_at)
 ORDER BY i.source,i.id DESC)
 SELECT COALESCE(max(id) FILTER(WHERE source='moysklad'),0),COALESCE(max(id) FILTER(WHERE source='ozon'),0),COALESCE(max(fingerprint) FILTER(WHERE source='moysklad'),''),COALESCE(max(fingerprint) FILTER(WHERE source='ozon'),''),count(*)=2 AND COALESCE(bool_and(COALESCE(finished_at BETWEEN clock_timestamp()-interval '1 hour' AND clock_timestamp() AND (source='moysklad' OR traversal_status='completed'),false)),false),(SELECT count(*) FROM ozon_product_observations WHERE import_id=(SELECT id FROM imports WHERE source='ozon')) FROM imports`, catalogScopeArgs(scope)...).Scan(&data.MoyID, &data.OzonID, &data.MoyFingerprint, &data.OzonFingerprint, &fresh, &data.Total)
	if err != nil {
		return data, fmt.Errorf("read matching imports: %w", err)
	}
	if !fresh {
		return data, catalogsync.ErrStale
	}
	return data, nil
}
