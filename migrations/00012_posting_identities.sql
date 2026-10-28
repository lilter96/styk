-- +goose Up
CREATE TABLE ozon_posting_identities (
 tenant_id text NOT NULL CHECK(length(trim(tenant_id))>0),
 account_id text NOT NULL CHECK(length(trim(account_id))>0),
 posting_number text NOT NULL CHECK(length(trim(posting_number))>0),
 order_id text NOT NULL CHECK(length(trim(order_id))>0),
 PRIMARY KEY (tenant_id,account_id,posting_number)
);
-- Conflicting historical identities fail this migration instead of being merged.
INSERT INTO ozon_posting_identities(tenant_id,account_id,posting_number,order_id)
SELECT DISTINCT b.tenant_id,b.account_id,o.posting_number,o.order_id
FROM ozon_posting_batches b JOIN ozon_posting_observations o ON o.batch_id=b.id;
-- +goose Down
DROP TABLE ozon_posting_identities;
