-- +goose Up
-- Derived index over immutable read evidence. This has no task, lease, attempt
-- scheduling or external-write state: the existing active reader repairs keys.
CREATE TABLE orders_read_repairs (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL CHECK(length(posting_number) BETWEEN 1 AND 256),
 failure_stream text NOT NULL CHECK(failure_stream IN ('ozon-orders-discovery','ozon-orders-unfulfilled')),
 failure_request_key text NOT NULL,
 failure_code text NOT NULL CHECK(failure_code IN ('POSTING_NOT_FOUND','POSTING_SCHEMA_DRIFT','POSTING_SCOPE_MISMATCH','POSTING_READ_FAILED','AUTHORIZATION_DENIED','RATE_LIMITED','PROVIDER_UNAVAILABLE','BUDGET_EXHAUSTED','READ_INTERRUPTED','READ_DEFERRED')),
 admitted_at timestamptz NOT NULL,
 failed_at timestamptz NOT NULL,
 resolved_at timestamptz,
 resolution_stream text,
 resolution_request_key text,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,posting_number),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account)
  REFERENCES orders_configs(tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,failure_stream,failure_request_key)
  REFERENCES orders_ingestion_pages(tenant_id,moysklad_account,ozon_account,stream,request_key) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,resolution_stream,resolution_request_key)
  REFERENCES orders_ingestion_pages(tenant_id,moysklad_account,ozon_account,stream,request_key) DEFERRABLE INITIALLY DEFERRED,
 CHECK(admitted_at<=failed_at),
 CHECK((resolved_at IS NULL)=(resolution_stream IS NULL)),
 CHECK((resolved_at IS NULL)=(resolution_request_key IS NULL)),
 CHECK(resolved_at IS NULL OR resolved_at>=failed_at)
);
CREATE INDEX orders_read_repairs_pending ON orders_read_repairs
 (tenant_id,moysklad_account,ozon_account,posting_number) WHERE resolved_at IS NULL;

-- Discovery failure fields were forbidden before62. Import only explicit
-- discovery/unfulfilled failures if an installation already retained them; old
-- active-window summary diagnostics are not invented per-point read evidence.
INSERT INTO orders_read_repairs(tenant_id,moysklad_account,ozon_account,posting_number,
 failure_stream,failure_request_key,failure_code,admitted_at,failed_at)
SELECT DISTINCT ON(p.tenant_id,p.moysklad_account,p.ozon_account,f->>'posting_number')
 p.tenant_id,p.moysklad_account,p.ozon_account,f->>'posting_number',p.stream,p.request_key,f->>'code',
 COALESCE((p.checkpoint->>'updated_at')::timestamptz,p.finished_at),
 COALESCE((p.checkpoint->>'updated_at')::timestamptz,p.finished_at)
FROM orders_ingestion_pages p CROSS JOIN LATERAL
 jsonb_array_elements(CASE WHEN jsonb_typeof(p.checkpoint->'page_failures')='array' THEN p.checkpoint->'page_failures'
 WHEN jsonb_typeof(p.checkpoint->'failures')='array' THEN p.checkpoint->'failures' ELSE '[]'::jsonb END) f
WHERE p.stream IN ('ozon-orders-discovery','ozon-orders-unfulfilled')
 AND length(f->>'posting_number') BETWEEN 1 AND 256
 AND f->>'code' IN ('POSTING_NOT_FOUND','POSTING_SCHEMA_DRIFT','POSTING_SCOPE_MISMATCH','POSTING_READ_FAILED','AUTHORIZATION_DENIED','RATE_LIMITED','PROVIDER_UNAVAILABLE','BUDGET_EXHAUSTED','READ_INTERRUPTED','READ_DEFERRED')
ORDER BY p.tenant_id,p.moysklad_account,p.ozon_account,f->>'posting_number',p.checkpoint_revision DESC;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_read_repairs) OR EXISTS(
  SELECT 1 FROM orders_ingestion_pages
  WHERE CASE WHEN jsonb_typeof(checkpoint->'page_failures')='array' THEN jsonb_array_length(checkpoint->'page_failures')>0 ELSE false END) THEN
  RAISE EXCEPTION 'order read-repair history prevents destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_read_repairs;
