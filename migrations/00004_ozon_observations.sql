-- +goose Up
CREATE TABLE ozon_import_batches (
 import_id bigint PRIMARY KEY REFERENCES source_imports(id) ON DELETE CASCADE,
 traversal_status text NOT NULL CHECK (traversal_status = 'completed')
);
CREATE TABLE ozon_product_observations (
 import_id bigint NOT NULL REFERENCES ozon_import_batches(import_id) ON DELETE CASCADE,
 product_id text NOT NULL,
 offer_id text NOT NULL,
 sku text,
 archived boolean,
 PRIMARY KEY (import_id, product_id),
 UNIQUE (import_id, offer_id)
);
CREATE TABLE ozon_warehouse_observations (
 import_id bigint NOT NULL REFERENCES ozon_import_batches(import_id) ON DELETE CASCADE,
 warehouse_id text NOT NULL,
 name text NOT NULL,
 status text NOT NULL,
 warehouse_type text NOT NULL,
 is_rfbs boolean,
 PRIMARY KEY (import_id, warehouse_id)
);
CREATE TABLE ozon_fbs_stock_observations (
 import_id bigint NOT NULL REFERENCES ozon_import_batches(import_id) ON DELETE CASCADE,
 product_id text NOT NULL,
 offer_id text NOT NULL,
 sku text,
 warehouse_id text NOT NULL,
 warehouse_name text NOT NULL,
 present bigint NOT NULL,
 reserved bigint NOT NULL,
 free_stock bigint NOT NULL,
 observed_at timestamptz NOT NULL,
 PRIMARY KEY (import_id, product_id, warehouse_id),
 FOREIGN KEY (import_id, product_id) REFERENCES ozon_product_observations(import_id, product_id)
);
-- +goose Down
DROP TABLE ozon_fbs_stock_observations;
DROP TABLE ozon_warehouse_observations;
DROP TABLE ozon_product_observations;
DROP TABLE ozon_import_batches;
