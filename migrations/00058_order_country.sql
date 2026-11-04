-- +goose Up
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('demand_create','salesreturn_create','mirror_create','mirror_update','mirror_position_update',
 'posting_pack','posting_ship','posting_cancel','posting_deliver','posting_split','posting_partial_cancel',
 'posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create',
 'posting_exemplar_prepare','posting_exemplar_set','posting_country_set'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_work WHERE action='posting_country_set') THEN
  RAISE EXCEPTION 'country declaration history prevents destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('demand_create','salesreturn_create','mirror_create','mirror_update','mirror_position_update',
 'posting_pack','posting_ship','posting_cancel','posting_deliver','posting_split','posting_partial_cancel',
 'posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create',
 'posting_exemplar_prepare','posting_exemplar_set'));
