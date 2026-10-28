-- +goose Up
ALTER TABLE ozon_posting_observations ADD COLUMN parent_posting_number text NOT NULL DEFAULT '', ADD COLUMN fact_delivery_date text NOT NULL DEFAULT '', ADD COLUMN shipment_date_without_delay text NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE ozon_posting_observations DROP COLUMN parent_posting_number, DROP COLUMN fact_delivery_date, DROP COLUMN shipment_date_without_delay;
