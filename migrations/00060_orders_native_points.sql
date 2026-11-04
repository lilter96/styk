-- +goose Up
ALTER TABLE orders_ingestion_leases ADD COLUMN attempt_started_at timestamptz;
ALTER TABLE orders_ingestion_pages ADD CONSTRAINT orders_ingestion_pages_native_scope UNIQUE(id,tenant_id,moysklad_account,ozon_account);
CREATE TABLE orders_native_point_snapshots (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 native_order_id text NOT NULL CHECK(length(native_order_id) BETWEEN 1 AND 512),
 target_revision text NOT NULL CHECK(length(target_revision) BETWEEN 1 AND 512),
 facts_hash text NOT NULL CHECK(facts_hash ~ '^[0-9a-f]{64}$'),
 point jsonb NOT NULL CHECK(jsonb_typeof(point)='object' AND octet_length(point::text)<=4194304),
 recorded_at timestamptz NOT NULL,
 CHECK((point->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account) AND point#>>'{correlation,posting_number}'=posting_number AND point#>>'{correlation,customer_order_id}'=native_order_id AND point->>'target_revision'=target_revision) IS TRUE),
 UNIQUE(tenant_id,moysklad_account,ozon_account,native_order_id,target_revision),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account,posting_number,native_order_id),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,posting_number) REFERENCES orders_projection_bindings(tenant_id,moysklad_account,ozon_account,posting_number)
);
CREATE TABLE orders_native_point_reads (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 native_order_id text NOT NULL CHECK(length(native_order_id) BETWEEN 1 AND 512),
 sync_id text NOT NULL,
 marker text NOT NULL,
 credential_revision bigint NOT NULL CHECK(credential_revision>0),
 page_id text NOT NULL,
 snapshot_id bigint,
 observed_at timestamptz,
 code text NOT NULL CHECK(code IN ('','NATIVE_READ_FAILED','NATIVE_AUTHORIZATION_DENIED','NATIVE_RATE_LIMITED','NATIVE_PROVIDER_UNAVAILABLE','NATIVE_ORDER_NOT_FOUND','NATIVE_ORDER_SCHEMA_DRIFT','NATIVE_BUDGET_EXHAUSTED','NATIVE_READ_INTERRUPTED','NATIVE_SCOPE_MISMATCH','NATIVE_ORDER_INCOMPLETE','NATIVE_BINDING_CHANGED','NATIVE_ORDER_CHANGED_DURING_READ')),
 admitted_at timestamptz NOT NULL,
 started_at timestamptz NOT NULL,
 finished_at timestamptz NOT NULL CHECK(finished_at>=started_at),
 recorded_at timestamptz NOT NULL,
 CHECK((snapshot_id IS NOT NULL)=(code='') AND (observed_at IS NOT NULL)=(code='')),
 CHECK(observed_at IS NULL OR (observed_at>=started_at AND observed_at<=finished_at)),
 UNIQUE(page_id,posting_number),
 FOREIGN KEY(page_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_ingestion_pages(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,posting_number) REFERENCES orders_projection_bindings(tenant_id,moysklad_account,ozon_account,posting_number),
 FOREIGN KEY(snapshot_id,tenant_id,moysklad_account,ozon_account,posting_number,native_order_id) REFERENCES orders_native_point_snapshots(id,tenant_id,moysklad_account,ozon_account,posting_number,native_order_id)
);
CREATE INDEX orders_native_point_latest ON orders_native_point_reads(tenant_id,moysklad_account,ozon_account,posting_number,finished_at DESC,id DESC);
CREATE INDEX orders_reconciliations_native_point_due ON orders_reconciliations(tenant_id,moysklad_account,ozon_account,(correlation->>'posting_number'),created_at DESC);
CREATE TRIGGER orders_native_point_snapshots_immutable BEFORE UPDATE OR DELETE ON orders_native_point_snapshots FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_native_point_reads_immutable BEFORE UPDATE OR DELETE ON orders_native_point_reads FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_native_point_reads) OR EXISTS(SELECT 1 FROM orders_native_point_snapshots) THEN
  RAISE EXCEPTION 'native order point history prevents rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE orders_ingestion_leases DROP COLUMN attempt_started_at;
DROP INDEX orders_reconciliations_native_point_due;
DROP TABLE orders_native_point_reads,orders_native_point_snapshots;
ALTER TABLE orders_ingestion_pages DROP CONSTRAINT orders_ingestion_pages_native_scope;
