package postgres

import (
	"context"
	"encoding/json"
)

func (s *Store) readProductMatchingExamples(ctx context.Context, j *ProductMatchingJob) error {
	for _, category := range []string{"exact", "attention"} {
		var raw []byte
		err := s.Pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(item),'[]'::jsonb) FROM(SELECT item FROM product_matching_job_pages jp JOIN product_matching_previews p ON p.id=jp.preview_id CROSS JOIN LATERAL jsonb_array_elements(p.preview->'items') item WHERE jp.job_id=$1 AND CASE WHEN $2='exact' THEN item->>'status'='exact' ELSE item->>'status' NOT IN ('exact','confirmed') END ORDER BY jp.page,item->>'product_id' LIMIT 3) samples`, j.ID, category).Scan(&raw)
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
