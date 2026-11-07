-- +goose Up
ALTER TABLE product_matching_jobs
 ADD COLUMN criterion text NOT NULL DEFAULT 'article_or_code' CHECK(criterion IN ('article_or_code','article','code')),
 ADD COLUMN apply_state text NOT NULL DEFAULT 'NOT_STARTED' CHECK(apply_state IN ('NOT_STARTED','QUEUED','RUNNING','COMPLETED','STALE','FAILED','CANCELLED')),
 ADD COLUMN apply_key text NOT NULL DEFAULT '',
 ADD COLUMN apply_total integer NOT NULL DEFAULT 0,
 ADD COLUMN apply_page integer NOT NULL DEFAULT 0,
 ADD COLUMN apply_offset integer NOT NULL DEFAULT 0,
 ADD COLUMN confirmed integer NOT NULL DEFAULT 0,
 ADD COLUMN skipped integer NOT NULL DEFAULT 0,
 ADD COLUMN apply_code text NOT NULL DEFAULT '';
CREATE INDEX product_matching_apply_queue ON product_matching_jobs(updated_at,id) WHERE apply_state IN ('QUEUED','RUNNING');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_matching_jobs WHERE apply_state<>'NOT_STARTED' OR criterion<>'article_or_code') THEN
  RAISE EXCEPTION 'Bulk matching evidence exists; retain migration 78 and use a forward fix';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX product_matching_apply_queue;
ALTER TABLE product_matching_jobs DROP COLUMN criterion, DROP COLUMN apply_state, DROP COLUMN apply_key,
 DROP COLUMN apply_total, DROP COLUMN apply_page, DROP COLUMN apply_offset, DROP COLUMN confirmed, DROP COLUMN skipped, DROP COLUMN apply_code;
