-- +goose Up
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('mirror_create','mirror_update','mirror_position_update','demand_create','salesreturn_create',
  'posting_pack','posting_ship','posting_cancel','posting_deliver','posting_split',
  'posting_partial_cancel','posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create'));

CREATE TABLE orders_native_document_intents (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('demand','salesreturn')),
 event_digest text NOT NULL CHECK(event_digest ~ '^[0-9a-f]{64}$'),
 sync_id uuid NOT NULL,
 marker text NOT NULL CHECK(length(marker) BETWEEN 1 AND 255),
 customer_order_id uuid NOT NULL,
 basis_document_id uuid,
 applicable boolean NOT NULL,
 created_at timestamptz NOT NULL,
 UNIQUE(work_id,tenant_id,moysklad_account,ozon_account),
 UNIQUE(work_id,tenant_id,moysklad_account,ozon_account,kind),
 UNIQUE(tenant_id,moysklad_account,kind,event_digest),
 UNIQUE(tenant_id,moysklad_account,kind,sync_id),
 UNIQUE(tenant_id,moysklad_account,kind,marker),
 CHECK((kind='demand')=(basis_document_id IS NULL)),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account)
  REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account)
);
CREATE TRIGGER orders_native_document_intents_no_update BEFORE UPDATE OR DELETE ON orders_native_document_intents
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

CREATE TABLE orders_native_document_targets (
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 store_id text NOT NULL CHECK(length(store_id) BETWEEN 1 AND 512),
 item_kind text NOT NULL CHECK(item_kind IN ('product','variant')),
 item_id text NOT NULL CHECK(length(item_id) BETWEEN 1 AND 512),
 quantity text NOT NULL CHECK(length(quantity) BETWEEN 1 AND 128 AND quantity ~ '^[0-9]+(\.[0-9]+)?$' AND quantity::numeric>0),
 native_order_position_id uuid NOT NULL,
 basis_position_id uuid,
 PRIMARY KEY(work_id,native_order_position_id),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account)
  REFERENCES orders_native_document_intents(work_id,tenant_id,moysklad_account,ozon_account)
);
CREATE INDEX orders_native_document_pool ON orders_native_document_targets(tenant_id,moysklad_account,store_id,item_kind,item_id,work_id);
CREATE TRIGGER orders_native_document_targets_no_update BEFORE UPDATE OR DELETE ON orders_native_document_targets
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

CREATE TABLE orders_native_document_results (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('demand','salesreturn')),
 native_document_id uuid NOT NULL,
 observed_at timestamptz NOT NULL,
 UNIQUE(tenant_id,moysklad_account,kind,native_document_id),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account,kind)
  REFERENCES orders_native_document_intents(work_id,tenant_id,moysklad_account,ozon_account,kind)
);
CREATE TRIGGER orders_native_document_results_no_update BEFORE UPDATE OR DELETE ON orders_native_document_results
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_native_document_intents) OR EXISTS(SELECT 1 FROM orders_work WHERE action IN ('demand_create','salesreturn_create')) THEN
  RAISE EXCEPTION 'native movement history prevents rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_native_document_results;
DROP TABLE orders_native_document_targets;
DROP TABLE orders_native_document_intents;
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('mirror_create','mirror_update','mirror_position_update','posting_pack','posting_ship','posting_cancel','posting_deliver',
  'posting_split','posting_partial_cancel','posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create'));
