-- +goose Up
ALTER TABLE ozon_posting_observations
 ADD COLUMN observed_at timestamptz,
 ADD COLUMN source_created_at timestamptz,
 ADD COLUMN source_updated_at timestamptz,
 ADD COLUMN cancellation_present boolean;
UPDATE ozon_posting_observations o SET observed_at=b.finished_at FROM ozon_posting_batches b WHERE b.id=o.batch_id;
ALTER TABLE ozon_posting_observations ALTER COLUMN observed_at SET NOT NULL;
ALTER TABLE ozon_posting_observations ALTER COLUMN cancellation_reason DROP NOT NULL,
 ALTER COLUMN cancellation_type DROP NOT NULL,
 ALTER COLUMN cancellation_initiator DROP NOT NULL;
CREATE INDEX ozon_posting_identity_idx ON ozon_posting_observations(posting_number,batch_id);
DROP VIEW ozon_posting_line_correlations;
DROP VIEW ozon_posting_warehouse_correlations;
-- +goose Down
CREATE VIEW ozon_posting_line_correlations AS
SELECT b.tenant_id,b.account_id,b.id AS batch_id,o.posting_number,l.line_no,l.offer_id,l.sku,l.quantity,m.moysklad_account,m.target_kind,m.target_id
FROM ozon_posting_batches b JOIN ozon_posting_observations o ON o.batch_id=b.id JOIN ozon_posting_lines l ON l.observation_id=o.id
LEFT JOIN product_mappings m ON m.tenant_id=b.tenant_id AND m.ozon_account=b.account_id AND m.ozon_offer_id=l.offer_id;
CREATE VIEW ozon_posting_warehouse_correlations AS
SELECT b.tenant_id,b.account_id,b.id AS batch_id,o.posting_number,o.warehouse_id,m.moysklad_account,m.moysklad_warehouse_id
FROM ozon_posting_batches b JOIN ozon_posting_observations o ON o.batch_id=b.id
LEFT JOIN warehouse_mappings m ON m.tenant_id=b.tenant_id AND m.ozon_account=b.account_id AND m.ozon_warehouse_id=o.warehouse_id AND m.status='active';
DROP INDEX ozon_posting_identity_idx;
UPDATE ozon_posting_observations SET cancellation_reason=coalesce(cancellation_reason,''), cancellation_type=coalesce(cancellation_type,''), cancellation_initiator=coalesce(cancellation_initiator,'');
ALTER TABLE ozon_posting_observations ALTER COLUMN cancellation_reason SET NOT NULL, ALTER COLUMN cancellation_type SET NOT NULL, ALTER COLUMN cancellation_initiator SET NOT NULL;
ALTER TABLE ozon_posting_observations DROP COLUMN observed_at, DROP COLUMN source_created_at, DROP COLUMN source_updated_at, DROP COLUMN cancellation_present;
