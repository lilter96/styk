-- +goose Up
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('mirror_create','mirror_update','mirror_position_update','posting_pack','posting_ship','posting_cancel','posting_deliver',
  'posting_split','posting_partial_cancel','posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create'));

CREATE INDEX orders_position_baseline_lookup ON orders_work(tenant_id,moysklad_account,ozon_account,posting_number,created_at DESC,id DESC)
 WHERE state='RECONCILED' AND action IN ('mirror_create','mirror_update','mirror_position_update');

-- +goose Down
-- Retain exact-target write history rather than silently deleting/reclassifying it.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_work WHERE action='mirror_position_update') THEN
  RAISE EXCEPTION 'position update history prevents rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX orders_position_baseline_lookup;
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('mirror_create','mirror_update','posting_pack','posting_ship','posting_cancel','posting_deliver',
  'posting_split','posting_partial_cancel','posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create'));
