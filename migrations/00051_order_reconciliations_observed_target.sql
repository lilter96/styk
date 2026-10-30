-- +goose Up
ALTER TABLE orders_reconciliations ADD COLUMN IF NOT EXISTS observed_target jsonb;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_reconciliations WHERE observed_target IS NOT NULL) THEN
  RAISE EXCEPTION 'reconciliation target data prevents rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE orders_reconciliations DROP COLUMN IF EXISTS observed_target;
