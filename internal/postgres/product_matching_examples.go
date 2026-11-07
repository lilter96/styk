package postgres

import (
	"context"
	"encoding/json"
)

func readProductMatchingExamples(ctx context.Context, q catalogQuerier, j *ProductMatchingJob) error {
	for _, category := range []string{"exact", "attention"} {
		var raw []byte
		err := q.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(item),'[]'::jsonb) FROM(SELECT item FROM product_matching_job_candidates WHERE job_id=$1 AND CASE WHEN $2='exact' THEN status='exact' ELSE status NOT IN ('exact','confirmed') END ORDER BY product_id LIMIT 3) samples`, j.ID, category).Scan(&raw)
		if err != nil {
			return err
		}
		target := &j.Examples
		if category == "attention" {
			target = &j.Attention
		}
		if err = json.Unmarshal(raw, target); err != nil {
			return err
		}
	}
	return nil
}
