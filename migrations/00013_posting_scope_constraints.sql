-- +goose Up
ALTER TABLE ozon_posting_batches ADD CONSTRAINT posting_batch_identity_scope UNIQUE (id,tenant_id,account_id);
ALTER TABLE ozon_posting_identities ADD CONSTRAINT posting_identity_order UNIQUE (tenant_id,account_id,posting_number,order_id);
ALTER TABLE ozon_posting_observations ADD COLUMN tenant_id text, ADD COLUMN account_id text;
UPDATE ozon_posting_observations o SET tenant_id=b.tenant_id,account_id=b.account_id FROM ozon_posting_batches b WHERE b.id=o.batch_id;
ALTER TABLE ozon_posting_observations ALTER COLUMN tenant_id SET NOT NULL, ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE ozon_posting_observations ADD CONSTRAINT posting_observation_batch_scope FOREIGN KEY(batch_id,tenant_id,account_id) REFERENCES ozon_posting_batches(id,tenant_id,account_id) ON DELETE CASCADE;
ALTER TABLE ozon_posting_observations ADD CONSTRAINT posting_observation_identity FOREIGN KEY(tenant_id,account_id,posting_number,order_id) REFERENCES ozon_posting_identities(tenant_id,account_id,posting_number,order_id);
-- +goose Down
ALTER TABLE ozon_posting_observations DROP CONSTRAINT posting_observation_identity, DROP CONSTRAINT posting_observation_batch_scope;
ALTER TABLE ozon_posting_observations DROP COLUMN tenant_id, DROP COLUMN account_id;
ALTER TABLE ozon_posting_identities DROP CONSTRAINT posting_identity_order;
ALTER TABLE ozon_posting_batches DROP CONSTRAINT posting_batch_identity_scope;
