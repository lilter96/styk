package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/matching"
)

const productMatchingSearchSQL = `WITH matches AS MATERIALIZED(SELECT * FROM product_matching_rows($2,$3,$4,$5,$6,$7)),
 inserted AS(INSERT INTO product_matching_job_candidates(job_id,product_id,status,target_kind,target_id,item)
 SELECT $1,product_id,status,target_kind,target_id,jsonb_build_object('product_id',product_id,'offer_id',offer_id,'name',name,'status',status,'reason',reason,'candidates',candidates,'shared_inventory',shared_inventory) FROM matches RETURNING status,item,product_id),
 counts AS(SELECT COALESCE(jsonb_object_agg(status,n),'{}'::jsonb) summary FROM(SELECT status,count(*) n FROM inserted GROUP BY status) s),
 examples AS(SELECT COALESCE(jsonb_agg(item),'[]'::jsonb) items FROM(SELECT item FROM inserted WHERE status='exact' ORDER BY product_id LIMIT 3) s),
 attention AS(SELECT COALESCE(jsonb_agg(item),'[]'::jsonb) items FROM(SELECT item FROM inserted WHERE status NOT IN ('exact','confirmed') ORDER BY product_id LIMIT 3) s),
 updated AS(UPDATE product_matching_jobs SET state='COMPLETED',processed=(SELECT count(*) FROM inserted),summary=(SELECT summary FROM counts),updated_at=clock_timestamp() WHERE id=$1 RETURNING processed,summary)
 SELECT u.processed,u.summary,e.items,a.items FROM updated u CROSS JOIN examples e CROSS JOIN attention a`

func completeProductMatchingSearch(ctx context.Context, tx pgx.Tx, j ProductMatchingJob, scope catalog.Scope, actor string, criterion matching.Criterion, data productMatchSnapshot) (ProductMatchingJob, error) {
	var examples, attention []byte
	err := tx.QueryRow(ctx, productMatchingSearchSQL, j.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, data.MoyID, data.OzonID, criterion.Fields()).Scan(&j.Processed, &j.Summary, &examples, &attention)
	if err != nil {
		return j, err
	}
	if err = catalogDecode(examples, &j.Examples); err != nil {
		return j, err
	}
	if err = catalogDecode(attention, &j.Attention); err != nil {
		return j, err
	}
	j.State = "COMPLETED"
	return j, nil
}

// The set pipeline must not become repeated scans when fresh import statistics
// underestimate its cardinality. Settings are local to this transaction.
func beginProductMatchingSet(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	return pool.BeginTx(ctx, pgx.TxOptions{BeginQuery: "BEGIN; SET LOCAL enable_nestloop=off; SET LOCAL jit=off"})
}
