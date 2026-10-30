-- +goose Up
ALTER TABLE orders_posting_heads ADD COLUMN IF NOT EXISTS projection jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(projection)='object');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_posting_heads WHERE projection <> '{}'::jsonb) THEN
  RAISE EXCEPTION 'order projection data prevents rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE orders_posting_heads DROP COLUMN IF EXISTS projection;
