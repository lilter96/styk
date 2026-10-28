-- +goose Up
CREATE INDEX source_warehouse_stock_console_idx ON source_warehouse_stock_observations (import_id, warehouse_id, kind, external_id);
CREATE INDEX ozon_fbs_stock_console_idx ON ozon_fbs_stock_observations (import_id, warehouse_id, product_id);

-- +goose Down
DROP INDEX ozon_fbs_stock_console_idx;
DROP INDEX source_warehouse_stock_console_idx;
