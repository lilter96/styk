-- +goose Up
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('mirror_create','mirror_update','mirror_position_update','demand_create','salesreturn_create',
  'posting_pack','posting_ship','posting_cancel','posting_deliver','posting_split',
  'posting_partial_cancel','posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create'));

ALTER TABLE orders_work ADD CONSTRAINT orders_work_native_action_key
 UNIQUE(id,tenant_id,moysklad_account,ozon_account,action);
ALTER TABLE orders_projection_bindings ADD CONSTRAINT orders_projection_native_parent_key
 UNIQUE(tenant_id,moysklad_account,ozon_account,posting_number,customer_order_id);
ALTER TABLE orders_work_warehouses ADD CONSTRAINT orders_native_work_store_key
 UNIQUE(work_id,tenant_id,moysklad_account,ozon_account,store_id);
ALTER TABLE orders_work_mappings ADD CONSTRAINT orders_native_work_item_key
 UNIQUE(work_id,mapping_id,tenant_id,moysklad_account,ozon_account,mapping_version,target_kind,target_id);

ALTER TABLE orders_reconciliations ADD COLUMN observed_native_document jsonb CHECK(observed_native_document IS NULL OR (jsonb_typeof(observed_native_document)='object' AND octet_length(observed_native_document::text)<=8388608));

CREATE TABLE orders_native_document_intents (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('demand','salesreturn')),
 action text GENERATED ALWAYS AS (kind || '_create') STORED,
 posting_number text NOT NULL,
 event_digest text NOT NULL CHECK(event_digest ~ '^[0-9a-f]{64}$'),
 sync_id uuid NOT NULL,
 marker text NOT NULL CHECK(length(marker) BETWEEN 1 AND 255),
 customer_order_id text NOT NULL CHECK(customer_order_id ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'),
 basis_document_id uuid,
 basis_kind text GENERATED ALWAYS AS (CASE WHEN kind='salesreturn' THEN 'demand'::text END) STORED,
 applicable boolean NOT NULL,
 created_at timestamptz NOT NULL,
 UNIQUE(work_id,tenant_id,moysklad_account,ozon_account),
 UNIQUE(work_id,tenant_id,moysklad_account,ozon_account,kind,posting_number,customer_order_id),
 UNIQUE(tenant_id,moysklad_account,kind,event_digest),
 UNIQUE(tenant_id,moysklad_account,kind,sync_id),
 UNIQUE(tenant_id,moysklad_account,kind,marker),
 CHECK((kind='demand')=(basis_document_id IS NULL)),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account,action)
  REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account,action),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,posting_number,customer_order_id)
  REFERENCES orders_projection_bindings(tenant_id,moysklad_account,ozon_account,posting_number,customer_order_id)
);
CREATE TRIGGER orders_native_document_intents_no_update BEFORE UPDATE OR DELETE ON orders_native_document_intents
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

CREATE TABLE orders_native_document_targets (
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 store_id text NOT NULL CHECK(length(store_id) BETWEEN 1 AND 512),
 mapping_id bigint NOT NULL,
 mapping_version integer NOT NULL CHECK(mapping_version>0),
 item_kind text NOT NULL CHECK(item_kind IN ('product','variant')),
 item_id text NOT NULL CHECK(length(item_id) BETWEEN 1 AND 512),
 quantity text NOT NULL CHECK(length(quantity) BETWEEN 1 AND 128 AND quantity ~ '^[0-9]+(\.[0-9]+)?$' AND quantity::numeric>0),
 native_order_position_id uuid NOT NULL,
 basis_position_id uuid,
 PRIMARY KEY(work_id,native_order_position_id),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account)
  REFERENCES orders_native_document_intents(work_id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account,store_id)
  REFERENCES orders_work_warehouses(work_id,tenant_id,moysklad_account,ozon_account,store_id),
 FOREIGN KEY(work_id,mapping_id,tenant_id,moysklad_account,ozon_account,mapping_version,item_kind,item_id)
  REFERENCES orders_work_mappings(work_id,mapping_id,tenant_id,moysklad_account,ozon_account,mapping_version,target_kind,target_id)
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
 posting_number text NOT NULL,
 customer_order_id text NOT NULL,
 observed_at timestamptz NOT NULL,
 UNIQUE(tenant_id,moysklad_account,kind,native_document_id),
 UNIQUE(tenant_id,moysklad_account,ozon_account,kind,native_document_id,posting_number,customer_order_id),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account,kind,posting_number,customer_order_id)
  REFERENCES orders_native_document_intents(work_id,tenant_id,moysklad_account,ozon_account,kind,posting_number,customer_order_id)
);
CREATE TRIGGER orders_native_document_results_no_update BEFORE UPDATE OR DELETE ON orders_native_document_results
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

ALTER TABLE orders_native_document_intents ADD CONSTRAINT orders_native_return_basis
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,basis_kind,basis_document_id,posting_number,customer_order_id)
  REFERENCES orders_native_document_results(tenant_id,moysklad_account,ozon_account,kind,native_document_id,posting_number,customer_order_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_reconciliations WHERE observed_native_document IS NOT NULL) OR EXISTS(SELECT 1 FROM orders_native_document_intents) OR EXISTS(SELECT 1 FROM orders_work WHERE action IN ('demand_create','salesreturn_create')) THEN
  RAISE EXCEPTION 'native movement history prevents rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE orders_native_document_intents DROP CONSTRAINT orders_native_return_basis;
DROP TABLE orders_native_document_results;
DROP TABLE orders_native_document_targets;
DROP TABLE orders_native_document_intents;
ALTER TABLE orders_reconciliations DROP COLUMN observed_native_document;
ALTER TABLE orders_work_mappings DROP CONSTRAINT orders_native_work_item_key;
ALTER TABLE orders_work_warehouses DROP CONSTRAINT orders_native_work_store_key;
ALTER TABLE orders_projection_bindings DROP CONSTRAINT orders_projection_native_parent_key;
ALTER TABLE orders_work DROP CONSTRAINT orders_work_native_action_key;
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('mirror_create','mirror_update','mirror_position_update','posting_pack','posting_ship','posting_cancel','posting_deliver',
  'posting_split','posting_partial_cancel','posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create'));
