-- +goose Up
-- Selection resumes from immutable automatic previews without scanning another
-- merchant's history; target history supports exact-posting attribution reads.
CREATE INDEX orders_previews_automatic_scope_latest ON orders_previews
 (tenant_id,moysklad_account,ozon_account,config_revision,actor,created_at DESC,id DESC)
 WHERE automatic;
CREATE INDEX orders_work_scoped_posting_latest ON orders_work
 (tenant_id,moysklad_account,ozon_account,posting_number,created_at DESC,id DESC);

-- +goose Down
DROP INDEX orders_work_scoped_posting_latest;
DROP INDEX orders_previews_automatic_scope_latest;
