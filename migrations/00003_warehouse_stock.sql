-- +goose Up
-- Existing aggregate observations remain in source_stock_observations.
CREATE TABLE source_warehouse_stock_observations (
 import_id bigint NOT NULL REFERENCES source_imports(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK (kind IN ('product', 'variant')),
 external_id text NOT NULL,
 source_href text NOT NULL,
 warehouse_id text NOT NULL,
 warehouse_href text NOT NULL,
 stock numeric NOT NULL,
 reserve numeric NOT NULL,
 in_transit numeric NOT NULL,
 quantity numeric,
 observed_at timestamptz NOT NULL,
 PRIMARY KEY (import_id, kind, external_id, warehouse_id)
);
-- +goose Down
DROP TABLE source_warehouse_stock_observations;
