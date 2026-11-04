-- +goose Up
-- Preserve the original split-and-assemble action and all of its history.
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('demand_create','salesreturn_create','mirror_create','mirror_update','mirror_position_update',
 'posting_pack','posting_ship','posting_cancel','posting_deliver','posting_split','posting_partial_cancel',
 'posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create',
 'posting_exemplar_prepare','posting_exemplar_set','posting_country_set','mirror_composition_update',
 'posting_split_unassembled'));

-- Normalized point-read progress avoids duplicating every child snapshot in
-- orders_work's bounded JSON. This is evidence, not another task queue.
CREATE TABLE orders_split_readbacks (
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL CHECK(length(posting_number) BETWEEN 1 AND 256),
 observation_id text,
 read_code text NOT NULL CHECK(read_code IN ('OBSERVED','SOURCE_UNAVAILABLE')),
 checked_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(work_id,posting_number),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account)
  REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(observation_id,tenant_id,moysklad_account,ozon_account,posting_number)
  REFERENCES orders_posting_observations(id,tenant_id,moysklad_account,ozon_account,posting_number),
 CHECK((read_code='OBSERVED')=(observation_id IS NOT NULL))
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_work WHERE action='posting_split_unassembled')
  OR EXISTS(SELECT 1 FROM orders_split_readbacks) THEN
  RAISE EXCEPTION 'unassembled split history prevents destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_split_readbacks;
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('demand_create','salesreturn_create','mirror_create','mirror_update','mirror_position_update',
 'posting_pack','posting_ship','posting_cancel','posting_deliver','posting_split','posting_partial_cancel',
 'posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create',
 'posting_exemplar_prepare','posting_exemplar_set','posting_country_set','mirror_composition_update'));
