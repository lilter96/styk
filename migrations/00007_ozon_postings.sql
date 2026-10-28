-- +goose Up
CREATE TABLE ozon_posting_batches (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, tenant_id text NOT NULL, account_id text NOT NULL, window_from timestamptz NOT NULL, window_to timestamptz NOT NULL, started_at timestamptz NOT NULL, finished_at timestamptz NOT NULL, traversal_status text NOT NULL CHECK(traversal_status='completed'));
CREATE INDEX ozon_posting_batches_scope_idx ON ozon_posting_batches(tenant_id,account_id,id DESC);
CREATE TABLE ozon_posting_observations (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, batch_id bigint NOT NULL REFERENCES ozon_posting_batches(id) ON DELETE CASCADE, posting_number text NOT NULL, order_id text NOT NULL, order_number text NOT NULL, status text NOT NULL, substatus text NOT NULL, warehouse_id text NOT NULL, in_process_at text NOT NULL, shipment_date text NOT NULL, delivering_date text NOT NULL, cancellation_reason text NOT NULL, cancellation_reason_id bigint, cancellation_type text NOT NULL, cancellation_initiator text NOT NULL, cancelled_after_ship boolean, UNIQUE(batch_id,posting_number));
CREATE TABLE ozon_posting_lines (observation_id bigint NOT NULL REFERENCES ozon_posting_observations(id) ON DELETE CASCADE, line_no integer NOT NULL, offer_id text NOT NULL, sku text NOT NULL, quantity bigint NOT NULL CHECK(quantity>=0), PRIMARY KEY(observation_id,line_no));
CREATE VIEW ozon_posting_line_correlations AS
SELECT b.tenant_id,b.account_id,b.id AS batch_id,o.posting_number,l.line_no,l.offer_id,l.sku,l.quantity,m.moysklad_account,m.target_kind,m.target_id
FROM ozon_posting_batches b JOIN ozon_posting_observations o ON o.batch_id=b.id JOIN ozon_posting_lines l ON l.observation_id=o.id
LEFT JOIN product_mappings m ON m.tenant_id=b.tenant_id AND m.ozon_account=b.account_id AND m.ozon_offer_id=l.offer_id;
CREATE VIEW ozon_posting_warehouse_correlations AS
SELECT b.tenant_id,b.account_id,b.id AS batch_id,o.posting_number,o.warehouse_id,m.moysklad_account,m.moysklad_warehouse_id
FROM ozon_posting_batches b JOIN ozon_posting_observations o ON o.batch_id=b.id
LEFT JOIN warehouse_mappings m ON m.tenant_id=b.tenant_id AND m.ozon_account=b.account_id AND m.ozon_warehouse_id=o.warehouse_id AND m.status='active';
-- +goose Down
DROP VIEW ozon_posting_warehouse_correlations;
DROP VIEW ozon_posting_line_correlations;
DROP TABLE ozon_posting_lines;
DROP TABLE ozon_posting_observations;
DROP TABLE ozon_posting_batches;
