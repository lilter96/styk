-- +goose Up
ALTER TABLE ozon_posting_batches
 ADD CONSTRAINT posting_batch_scope_not_blank CHECK (length(trim(tenant_id))>0 AND length(trim(account_id))>0),
 ADD CONSTRAINT posting_batch_window_valid CHECK (window_to>window_from);
ALTER TABLE ozon_posting_observations
 ADD CONSTRAINT posting_identity_not_blank CHECK (length(trim(posting_number))>0 AND length(trim(order_id))>0 AND length(trim(order_number))>0 AND length(trim(status))>0);
ALTER TABLE ozon_posting_lines
 ADD CONSTRAINT posting_line_identity_not_blank CHECK (length(trim(offer_id))>0 OR length(trim(sku))>0);
-- +goose Down
ALTER TABLE ozon_posting_lines DROP CONSTRAINT posting_line_identity_not_blank;
ALTER TABLE ozon_posting_observations DROP CONSTRAINT posting_identity_not_blank;
ALTER TABLE ozon_posting_batches DROP CONSTRAINT posting_batch_scope_not_blank, DROP CONSTRAINT posting_batch_window_valid;
