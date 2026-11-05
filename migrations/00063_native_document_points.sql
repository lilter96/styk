-- +goose Up
ALTER TABLE orders_native_document_results ADD CONSTRAINT orders_native_document_point_key
 UNIQUE(work_id,tenant_id,moysklad_account,ozon_account,kind,native_document_id,posting_number,customer_order_id);
CREATE INDEX orders_native_document_point_queue ON orders_native_document_results
 (tenant_id,moysklad_account,ozon_account,work_id);

CREATE TABLE orders_native_document_snapshots (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 kind text NOT NULL CHECK(kind IN('demand','salesreturn')),
 native_document_id uuid NOT NULL,
 posting_number text NOT NULL,
 customer_order_id text NOT NULL,
 facts_hash text NOT NULL CHECK(facts_hash ~ '^[0-9a-f]{64}$'),
 observation jsonb NOT NULL CHECK(jsonb_typeof(observation)='object' AND octet_length(observation::text)<=8388608),
 recorded_at timestamptz NOT NULL,
 CHECK((observation->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account)
  AND observation#>>'{correlation,kind}'=kind AND observation#>>'{correlation,document_id}'=native_document_id::text) IS TRUE),
 UNIQUE(work_id,facts_hash),
 UNIQUE(id,work_id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account,kind,native_document_id,posting_number,customer_order_id)
  REFERENCES orders_native_document_results(work_id,tenant_id,moysklad_account,ozon_account,kind,native_document_id,posting_number,customer_order_id)
);

CREATE TABLE orders_native_document_reads (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 kind text NOT NULL CHECK(kind IN('demand','salesreturn')),
 native_document_id uuid NOT NULL,
 posting_number text NOT NULL,
 customer_order_id text NOT NULL,
 credential_revision bigint NOT NULL CHECK(credential_revision>0),
 page_id text NOT NULL,
 snapshot_id bigint,
 code text NOT NULL,
 started_at timestamptz NOT NULL,
 finished_at timestamptz NOT NULL CHECK(finished_at>=started_at),
 observed_at timestamptz,
 admitted_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL,
 CHECK((snapshot_id IS NOT NULL)=(code='') AND (observed_at IS NOT NULL)=(code='')),
 CHECK(observed_at IS NULL OR observed_at BETWEEN started_at AND finished_at),
 CHECK(code IN('','NATIVE_READ_FAILED','NATIVE_AUTHORIZATION_DENIED','NATIVE_RATE_LIMITED','NATIVE_PROVIDER_UNAVAILABLE',
  'NATIVE_ORDER_NOT_FOUND','NATIVE_ORDER_SCHEMA_DRIFT','NATIVE_BUDGET_EXHAUSTED','NATIVE_READ_INTERRUPTED',
  'NATIVE_SCOPE_MISMATCH','NATIVE_ORDER_INCOMPLETE','NATIVE_BINDING_CHANGED','NATIVE_ORDER_CHANGED_DURING_READ')),
 UNIQUE(page_id,work_id),
 FOREIGN KEY(page_id,tenant_id,moysklad_account,ozon_account)
  REFERENCES orders_ingestion_pages(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account,kind,native_document_id,posting_number,customer_order_id)
  REFERENCES orders_native_document_results(work_id,tenant_id,moysklad_account,ozon_account,kind,native_document_id,posting_number,customer_order_id),
 FOREIGN KEY(snapshot_id,work_id,tenant_id,moysklad_account,ozon_account)
  REFERENCES orders_native_document_snapshots(id,work_id,tenant_id,moysklad_account,ozon_account)
);
CREATE INDEX orders_native_document_latest ON orders_native_document_reads
 (tenant_id,moysklad_account,ozon_account,work_id,admitted_at DESC,id DESC);
CREATE TRIGGER orders_native_document_snapshots_immutable BEFORE UPDATE OR DELETE ON orders_native_document_snapshots
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_native_document_reads_immutable BEFORE UPDATE OR DELETE ON orders_native_document_reads
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_native_document_reads) OR EXISTS(SELECT 1 FROM orders_native_document_snapshots) THEN
  RAISE EXCEPTION 'native document point history prevents rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_native_document_reads,orders_native_document_snapshots;
DROP INDEX orders_native_document_point_queue;
ALTER TABLE orders_native_document_results DROP CONSTRAINT orders_native_document_point_key;
