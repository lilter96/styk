-- +goose Up
ALTER TABLE ozon_posting_observations ADD COLUMN observed_time_basis text NOT NULL DEFAULT 'batch_finished' CHECK (observed_time_basis IN ('detail_received','batch_finished'));
-- +goose Down
ALTER TABLE ozon_posting_observations DROP COLUMN observed_time_basis;
