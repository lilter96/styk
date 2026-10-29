-- +goose Up
-- Orders share the transactional outbox. A dispatched command is never retried
-- as a write; only a read may reconcile an uncertain provider outcome.
ALTER TABLE saas_merchant_accounts ADD CONSTRAINT orders_merchant_owner_scope UNIQUE(tenant_id,user_id);

CREATE TABLE orders_configs (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 moysklad_provider text GENERATED ALWAYS AS ('moysklad'::text) STORED,
 ozon_provider text GENERATED ALWAYS AS ('ozon'::text) STORED,
 revision bigint NOT NULL CHECK(revision>0),
 enabled boolean NOT NULL,
 next_run_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_provider,moysklad_account) REFERENCES connection_credentials(tenant_id,provider,account_id),
 FOREIGN KEY(tenant_id,ozon_provider,ozon_account) REFERENCES connection_credentials(tenant_id,provider,account_id)
);
CREATE TABLE orders_config_revisions (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 config jsonb NOT NULL CHECK(jsonb_typeof(config)='object' AND octet_length(config::text)<=1048576),
 actor text NOT NULL REFERENCES saas_sessions(audit_id),
 owner_user_id text NOT NULL,
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,revision),
 UNIQUE(tenant_id,moysklad_account,ozon_account,request_key),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES saas_merchant_accounts(tenant_id,user_id),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES orders_configs(tenant_id,moysklad_account,ozon_account) DEFERRABLE INITIALLY DEFERRED,
 CHECK((config->>'revision'=revision::text AND config->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account)) IS TRUE)
);
ALTER TABLE orders_configs ADD CONSTRAINT orders_current_config_fk FOREIGN KEY(tenant_id,moysklad_account,ozon_account,revision) REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX orders_configs_due ON orders_configs(next_run_at,tenant_id) WHERE enabled;

-- Stable owner grants are independent of the authorizing browser session.
CREATE TABLE orders_automation_grants (
 id text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 owner_user_id text NOT NULL,
 grant_record jsonb NOT NULL CHECK(jsonb_typeof(grant_record)='object'),
 granted_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>granted_at),
 revoked_at timestamptz,
 PRIMARY KEY(id,revision),
 UNIQUE(id,revision,tenant_id,moysklad_account,ozon_account,config_revision),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES saas_merchant_accounts(tenant_id,user_id),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision),
 CHECK((grant_record->>'id'=id AND grant_record->>'revision'=revision::text AND grant_record->>'config_revision'=config_revision::text AND grant_record->>'owner_id'=owner_user_id AND grant_record->>'source_account'=ozon_account AND grant_record->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account)) IS TRUE)
);

CREATE TABLE orders_commercial_orders (
 tenant_id text NOT NULL,
 ozon_account text NOT NULL,
 commercial_order_id text NOT NULL CHECK(length(commercial_order_id) BETWEEN 1 AND 256),
 provider text GENERATED ALWAYS AS ('ozon'::text) STORED,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,ozon_account,commercial_order_id),
 FOREIGN KEY(tenant_id,provider,ozon_account) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE TABLE orders_posting_identities (
 tenant_id text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL CHECK(length(posting_number) BETWEEN 1 AND 256),
 commercial_order_id text NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,ozon_account,posting_number),
 UNIQUE(tenant_id,ozon_account,posting_number,commercial_order_id),
 FOREIGN KEY(tenant_id,ozon_account,commercial_order_id) REFERENCES orders_commercial_orders(tenant_id,ozon_account,commercial_order_id)
);
CREATE TABLE orders_posting_observations (
 id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 commercial_order_id text NOT NULL,
 source_revision text NOT NULL CHECK(length(source_revision) BETWEEN 1 AND 256),
 observation_hash text NOT NULL CHECK(observation_hash ~ '^[0-9a-f]{64}$'),
 posting jsonb NOT NULL CHECK(jsonb_typeof(posting)='object' AND octet_length(posting::text)<=1048576),
 observed_at timestamptz NOT NULL,
 provider_updated_at timestamptz,
 created_at timestamptz NOT NULL,
 UNIQUE(id,tenant_id,moysklad_account,ozon_account,posting_number),
 UNIQUE(tenant_id,moysklad_account,ozon_account,posting_number,source_revision),
 FOREIGN KEY(tenant_id,ozon_account,posting_number,commercial_order_id) REFERENCES orders_posting_identities(tenant_id,ozon_account,posting_number,commercial_order_id),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES orders_configs(tenant_id,moysklad_account,ozon_account),
 CHECK((posting->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account) AND posting->>'posting_number'=posting_number AND posting->>'commercial_order_id'=commercial_order_id AND posting#>>'{evidence,revision}'=source_revision) IS TRUE)
);
CREATE INDEX orders_posting_history ON orders_posting_observations(tenant_id,moysklad_account,ozon_account,posting_number,created_at DESC,id);
CREATE INDEX orders_posting_facts ON orders_posting_observations(tenant_id,moysklad_account,ozon_account,posting_number,observation_hash);
CREATE TABLE orders_posting_heads (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 observation_id text NOT NULL,
 projection jsonb NOT NULL CHECK(jsonb_typeof(projection)='object'),
 active boolean NOT NULL,
 ordering text NOT NULL DEFAULT 'unknown' CHECK(ordering IN ('same','newer','unknown')),
 quarantined boolean NOT NULL DEFAULT false,
 checked_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,posting_number),
 FOREIGN KEY(observation_id,tenant_id,moysklad_account,ozon_account,posting_number) REFERENCES orders_posting_observations(id,tenant_id,moysklad_account,ozon_account,posting_number)
);
CREATE INDEX orders_posting_active ON orders_posting_heads(tenant_id,moysklad_account,ozon_account,posting_number) WHERE active;
CREATE TABLE orders_posting_head_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 observation_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 ordering text NOT NULL,
 created_at timestamptz NOT NULL,
 FOREIGN KEY(observation_id,tenant_id,moysklad_account,ozon_account,posting_number) REFERENCES orders_posting_observations(id,tenant_id,moysklad_account,ozon_account,posting_number)
);
CREATE INDEX orders_posting_head_event_scope ON orders_posting_head_events(tenant_id,moysklad_account,ozon_account,posting_number,id DESC);
CREATE TABLE orders_posting_conflicts (
 id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 current_observation_id text NOT NULL,
 incoming_observation_id text NOT NULL,
 collection_sequence bigint NOT NULL,
 code text NOT NULL CHECK(code IN ('ORDERING_SUPERSEDED_OBSERVATION','ORDERING_PROVIDER_REVISION_CONFLICT')),
 created_at timestamptz NOT NULL,
 FOREIGN KEY(current_observation_id,tenant_id,moysklad_account,ozon_account,posting_number) REFERENCES orders_posting_observations(id,tenant_id,moysklad_account,ozon_account,posting_number),
 FOREIGN KEY(incoming_observation_id,tenant_id,moysklad_account,ozon_account,posting_number) REFERENCES orders_posting_observations(id,tenant_id,moysklad_account,ozon_account,posting_number)
);
CREATE TABLE orders_line_identities (
 tenant_id text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 line_key text NOT NULL,
 provider_line_id text NOT NULL,
 sku text NOT NULL,
 offer_id text NOT NULL,
 PRIMARY KEY(tenant_id,ozon_account,posting_number,line_key),
 FOREIGN KEY(tenant_id,ozon_account,posting_number) REFERENCES orders_posting_identities(tenant_id,ozon_account,posting_number)
);
CREATE UNIQUE INDEX orders_line_provider_identity ON orders_line_identities(tenant_id,ozon_account,posting_number,provider_line_id) WHERE provider_line_id<>'';

CREATE TABLE orders_ingestion_leases (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 stream text NOT NULL,
 refresh_key text NOT NULL,
 sequence bigint NOT NULL CHECK(sequence>0),
 collection_sequence bigint NOT NULL DEFAULT 0 CHECK(collection_sequence>=0),
 token text,
 worker_id text NOT NULL,
 config_revision bigint NOT NULL,
 ozon_revision bigint NOT NULL,
 moysklad_revision bigint NOT NULL,
 lease_until timestamptz,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account),
 CHECK((token IS NULL)=(lease_until IS NULL)),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision)
);

CREATE TABLE orders_checkpoints (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 stream text NOT NULL CHECK(length(stream) BETWEEN 1 AND 128),
 revision bigint NOT NULL CHECK(revision>0),
 checkpoint jsonb NOT NULL CHECK(jsonb_typeof(checkpoint)='object'),
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,stream),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES orders_configs(tenant_id,moysklad_account,ozon_account),
 CHECK((checkpoint->>'revision'=revision::text AND checkpoint->>'stream'=stream AND checkpoint->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account)) IS TRUE)
);
CREATE TABLE orders_ingestion_pages (
 id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 stream text NOT NULL,
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 checkpoint_revision bigint NOT NULL,
 checkpoint jsonb NOT NULL CHECK(jsonb_typeof(checkpoint)='object'),
 observation_ids text[] NOT NULL,
 started_at timestamptz NOT NULL,
 finished_at timestamptz NOT NULL CHECK(finished_at>=started_at),
 UNIQUE(tenant_id,moysklad_account,ozon_account,stream,request_key),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,stream) REFERENCES orders_checkpoints(tenant_id,moysklad_account,ozon_account,stream)
);

-- Only encrypted PII belongs here. Neither postings nor work contains a raw
-- provider payload. The application binds purpose, scope, record and revision.
CREATE TABLE orders_posting_secrets (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 purpose text NOT NULL CHECK(length(purpose) BETWEEN 1 AND 128),
 ciphertext bytea NOT NULL CHECK(octet_length(ciphertext) BETWEEN 29 AND 1048604),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>created_at AND expires_at<=created_at+interval '30 days'),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,posting_number,revision),
 FOREIGN KEY(tenant_id,ozon_account,posting_number) REFERENCES orders_posting_identities(tenant_id,ozon_account,posting_number),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES orders_configs(tenant_id,moysklad_account,ozon_account)
);

CREATE TABLE orders_projection_bindings (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 sync_id text NOT NULL CHECK(length(sync_id) BETWEEN 1 AND 256),
 marker text NOT NULL CHECK(length(marker) BETWEEN 1 AND 512),
 customer_order_id text,
 created_at timestamptz NOT NULL,
 resolved_at timestamptz,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,posting_number),
 UNIQUE(tenant_id,moysklad_account,sync_id),
 UNIQUE(tenant_id,moysklad_account,marker),
 UNIQUE(tenant_id,moysklad_account,customer_order_id),
 FOREIGN KEY(tenant_id,ozon_account,posting_number) REFERENCES orders_posting_identities(tenant_id,ozon_account,posting_number),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES orders_configs(tenant_id,moysklad_account,ozon_account),
 CHECK((customer_order_id IS NULL)=(resolved_at IS NULL))
);
CREATE TABLE orders_previews (
 id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 preview jsonb NOT NULL CHECK(jsonb_typeof(preview)='object' AND octet_length(preview::text)<=16777216),
 fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 actor text NOT NULL,
 automatic boolean NOT NULL,
 owner_user_id text NOT NULL,
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>created_at),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account,config_revision),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES saas_merchant_accounts(tenant_id,user_id),
 CHECK((preview->>'id'=id AND preview->>'config_revision'=config_revision::text AND preview->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account)) IS TRUE)
);
CREATE TABLE orders_operations (
 id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 preview_id text NOT NULL,
 actor text NOT NULL,
 owner_user_id text NOT NULL,
 automatic boolean NOT NULL,
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL,
 UNIQUE(id,tenant_id,moysklad_account,ozon_account),
 UNIQUE(tenant_id,moysklad_account,ozon_account,request_key),
 FOREIGN KEY(preview_id,tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES orders_previews(id,tenant_id,moysklad_account,ozon_account,config_revision),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES saas_merchant_accounts(tenant_id,user_id)
);
CREATE INDEX orders_operation_history ON orders_operations(tenant_id,moysklad_account,ozon_account,created_at DESC,id DESC);
CREATE TABLE orders_work (
 id text PRIMARY KEY,
 operation_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 posting_number text NOT NULL,
 command_id text NOT NULL UNIQUE,
 command_fingerprint text NOT NULL CHECK(command_fingerprint ~ '^[0-9a-f]{64}$'),
 action text NOT NULL CHECK(action IN ('mirror_create','mirror_update','posting_pack','posting_ship','posting_cancel','posting_deliver')),
 state text NOT NULL CHECK(state IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','RECONCILED','UNKNOWN','REJECTED','BLOCKED')),
 work jsonb NOT NULL CHECK(jsonb_typeof(work)='object' AND octet_length(work::text)<=2097152),
 dispatch_count integer NOT NULL DEFAULT 0 CHECK(dispatch_count BETWEEN 0 AND 1),
 dispatched_at timestamptz,
 finished_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 UNIQUE(id,tenant_id,moysklad_account,ozon_account),
 UNIQUE(id,operation_id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(operation_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_operations(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,posting_number) REFERENCES orders_projection_bindings(tenant_id,moysklad_account,ozon_account,posting_number),
 CHECK((dispatch_count=0)=(dispatched_at IS NULL)),
 CHECK((work->>'id'=id AND work->>'operation_id'=operation_id AND work->>'config_revision'=config_revision::text AND work->>'state'=state AND work->>'command_fingerprint'=command_fingerprint AND work#>>'{command,id}'=command_id AND work#>>'{command,posting_number}'=posting_number AND work#>>'{command,action}'=action AND work->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account)) IS TRUE)
);
CREATE UNIQUE INDEX orders_work_active_target ON orders_work(tenant_id,moysklad_account,ozon_account,posting_number) WHERE state IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN');
CREATE INDEX orders_work_operation ON orders_work(operation_id,id);
CREATE TABLE orders_work_mappings (
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 mapping_id bigint NOT NULL,
 mapping_version integer NOT NULL,
 target_kind text NOT NULL,
 target_id text NOT NULL,
 product_id text NOT NULL,
 offer_id text NOT NULL,
 PRIMARY KEY(work_id,mapping_id),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(mapping_id,mapping_version,tenant_id,ozon_account,moysklad_account,target_kind,target_id,product_id,offer_id) REFERENCES product_mapping_history(mapping_id,version,tenant_id,ozon_account,moysklad_account,target_kind,target_id,ozon_product_id,ozon_offer_id)
);
CREATE TABLE orders_work_warehouses (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 mapping_id bigint NOT NULL,
 mapping_version integer NOT NULL,
 store_id text NOT NULL,
 warehouse_id text NOT NULL,
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(mapping_id,mapping_version,tenant_id,ozon_account,moysklad_account,store_id,warehouse_id) REFERENCES warehouse_mapping_history(mapping_id,version,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id)
);
CREATE TABLE orders_write_intents (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 command jsonb NOT NULL CHECK(jsonb_typeof(command)='object'),
 fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL,
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account)
);
CREATE TABLE orders_attempts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 phase text NOT NULL,
 code text NOT NULL CHECK(length(code)<=128 AND code ~ '^[A-Z0-9_]*$'),
 created_at timestamptz NOT NULL,
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account)
);
CREATE TABLE orders_receipts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL,
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account)
);
CREATE TABLE orders_reconciliations (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 correlation jsonb NOT NULL CHECK(jsonb_typeof(correlation)='object'),
 observed_posting jsonb,
 observed_target jsonb,
 created_at timestamptz NOT NULL,
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account)
);

CREATE TABLE orders_metadata_revisions (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 provider text GENERATED ALWAYS AS ('moysklad'::text) STORED,
 registry jsonb NOT NULL CHECK(jsonb_typeof(registry)='object'),
 observed_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,moysklad_account,revision),
 FOREIGN KEY(tenant_id,provider,moysklad_account) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE TABLE orders_metadata_attributes (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 unique_code text NOT NULL CHECK(length(unique_code) BETWEEN 1 AND 256),
 attribute_id text NOT NULL CHECK(attribute_id ~ '^[0-9a-fA-F-]{36}$'),
 attribute_type text NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,moysklad_account,unique_code),
 UNIQUE(tenant_id,moysklad_account,attribute_id),
 provider text GENERATED ALWAYS AS ('moysklad'::text) STORED,
 FOREIGN KEY(tenant_id,provider,moysklad_account) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE TABLE orders_metadata_creation_intents (
 id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 unique_code text NOT NULL,
 attribute_type text NOT NULL,
 fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 intent jsonb NOT NULL CHECK(jsonb_typeof(intent)='object'),
 state text NOT NULL CHECK(state IN ('DISPATCHING','UNKNOWN','REJECTED','RECONCILED')),
 attribute_id text,
 actor text NOT NULL REFERENCES saas_sessions(audit_id),
 owner_user_id text NOT NULL,
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL,
 provider text GENERATED ALWAYS AS ('moysklad'::text) STORED,
 UNIQUE(tenant_id,moysklad_account,unique_code),
 UNIQUE(tenant_id,moysklad_account,request_key),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES saas_merchant_accounts(tenant_id,user_id),
 FOREIGN KEY(tenant_id,provider,moysklad_account) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE TABLE orders_metadata_creation_results (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 intent_id text NOT NULL REFERENCES orders_metadata_creation_intents(id),
 result jsonb NOT NULL CHECK(jsonb_typeof(result)='object'),
 created_at timestamptz NOT NULL
);

ALTER TABLE outbox_tasks ADD COLUMN orders_operation_id text, ADD COLUMN orders_work_id text, ADD COLUMN orders_tenant_id text, ADD COLUMN orders_moysklad_account text, ADD COLUMN orders_ozon_account text;
ALTER TABLE outbox_tasks ADD CONSTRAINT orders_outbox_scope_fk FOREIGN KEY(orders_work_id,orders_operation_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account) REFERENCES orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account);
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK(task_type IN ('mock','ozon_catalog_import','moysklad_catalog_import','stock_publication','catalog_sync','orders_sync'));
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK(
 (task_type='mock' AND state<>'cancelled' AND observation_id IS NOT NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL)
 OR (task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND observation_id IS NULL AND import_operation_id IS NOT NULL AND import_tenant_id IS NOT NULL AND import_account_id IS NOT NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL)
 OR (task_type='stock_publication' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NOT NULL AND max_attempts=1 AND attempts<=1 AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL)
 OR (task_type='catalog_sync' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NOT NULL AND catalog_work_id IS NOT NULL AND catalog_tenant_id IS NOT NULL AND catalog_moysklad_account IS NOT NULL AND catalog_ozon_account IS NOT NULL AND max_attempts=1 AND attempts<=1)
 OR (task_type='orders_sync' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL AND max_attempts=1 AND attempts<=1));
ALTER TABLE outbox_tasks ADD CONSTRAINT orders_outbox_payload_check CHECK(
 (task_type='orders_sync' AND orders_operation_id IS NOT NULL AND orders_work_id IS NOT NULL AND orders_tenant_id IS NOT NULL AND orders_moysklad_account IS NOT NULL AND orders_ozon_account IS NOT NULL)
 OR (task_type<>'orders_sync' AND orders_operation_id IS NULL AND orders_work_id IS NULL AND orders_tenant_id IS NULL AND orders_moysklad_account IS NULL AND orders_ozon_account IS NULL));
CREATE UNIQUE INDEX orders_outbox_work ON outbox_tasks(orders_work_id) WHERE task_type='orders_sync';
CREATE INDEX orders_outbox_claim ON outbox_tasks(available_at,id) WHERE task_type='orders_sync' AND state IN ('pending','running');

-- +goose StatementBegin
CREATE FUNCTION orders_config_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'order configuration history is immutable' USING ERRCODE='55000'; END IF;
 IF (NEW.tenant_id,NEW.moysklad_account,NEW.ozon_account) IS DISTINCT FROM (OLD.tenant_id,OLD.moysklad_account,OLD.ozon_account) OR NEW.revision NOT IN (OLD.revision,OLD.revision+1) OR (NEW.revision=OLD.revision AND NEW.enabled<>OLD.enabled) THEN RAISE EXCEPTION 'order configuration revision is invalid' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_config_guard BEFORE UPDATE OR DELETE ON orders_configs FOR EACH ROW EXECUTE FUNCTION orders_config_guard();

-- +goose StatementBegin
CREATE FUNCTION orders_actor_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM saas_require_actor(NEW.tenant_id,NEW.actor,true);
 IF NOT EXISTS(SELECT 1 FROM saas_sessions WHERE audit_id=NEW.actor AND user_id=NEW.owner_user_id) THEN RAISE EXCEPTION 'order actor owner mismatch' USING ERRCODE='42501'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_config_actor BEFORE INSERT ON orders_config_revisions FOR EACH ROW EXECUTE FUNCTION orders_actor_guard();
CREATE TRIGGER orders_metadata_actor BEFORE INSERT ON orders_metadata_creation_intents FOR EACH ROW EXECUTE FUNCTION orders_actor_guard();

-- +goose StatementBegin
CREATE FUNCTION orders_metadata_intent_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'order metadata creation history is immutable' USING ERRCODE='55000'; END IF;
 IF (to_jsonb(NEW)-'state'-'attribute_id'-'intent'-'provider') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'attribute_id'-'intent'-'provider') OR (NEW.intent-'state'-'attribute_id') IS DISTINCT FROM (OLD.intent-'state'-'attribute_id') THEN RAISE EXCEPTION 'order metadata creation intent is immutable' USING ERRCODE='55000'; END IF;
 IF OLD.state IN ('RECONCILED','REJECTED') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'terminal metadata creation is immutable' USING ERRCODE='55000'; END IF;
 IF NEW.state NOT IN ('UNKNOWN','REJECTED','RECONCILED') OR (NEW.state='RECONCILED' AND NEW.attribute_id IS NULL) THEN RAISE EXCEPTION 'metadata creation cannot be replayed' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_metadata_intent_guard BEFORE UPDATE OR DELETE ON orders_metadata_creation_intents FOR EACH ROW EXECUTE FUNCTION orders_metadata_intent_guard();

-- +goose StatementBegin
CREATE FUNCTION orders_binding_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'order projection binding is immutable' USING ERRCODE='55000'; END IF;
 IF (to_jsonb(NEW)-'customer_order_id'-'resolved_at') IS DISTINCT FROM (to_jsonb(OLD)-'customer_order_id'-'resolved_at') OR (OLD.customer_order_id IS NOT NULL AND NEW IS DISTINCT FROM OLD) THEN RAISE EXCEPTION 'order projection identity is immutable' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_binding_guard BEFORE UPDATE OR DELETE ON orders_projection_bindings FOR EACH ROW EXECUTE FUNCTION orders_binding_guard();

-- +goose StatementBegin
CREATE FUNCTION orders_work_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'order work history is immutable' USING ERRCODE='55000'; END IF;
 IF (to_jsonb(NEW)-'state'-'work'-'dispatch_count'-'dispatched_at'-'finished_at'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'work'-'dispatch_count'-'dispatched_at'-'finished_at'-'updated_at') OR (NEW.work-'state'-'receipt'-'lease_until'-'dispatch_count'-'next_run_at'-'code') IS DISTINCT FROM (OLD.work-'state'-'receipt'-'lease_until'-'dispatch_count'-'next_run_at'-'code') THEN RAISE EXCEPTION 'order write intent is immutable' USING ERRCODE='55000'; END IF;
 IF NEW.dispatch_count<OLD.dispatch_count OR (OLD.dispatched_at IS NOT NULL AND NEW.dispatched_at IS DISTINCT FROM OLD.dispatched_at) THEN RAISE EXCEPTION 'order dispatch fence cannot reset' USING ERRCODE='55000'; END IF;
 IF OLD.state IN ('RECONCILED','REJECTED','BLOCKED') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'terminal order work is immutable' USING ERRCODE='55000'; END IF;
 IF NEW.state<>OLD.state AND NOT (
  (OLD.state='QUEUED' AND NEW.state IN ('CLAIMED','BLOCKED')) OR
  (OLD.state='CLAIMED' AND NEW.state IN ('QUEUED','DISPATCHING','BLOCKED','UNKNOWN','RECONCILED')) OR
  (OLD.state='DISPATCHING' AND NEW.state IN ('ACKNOWLEDGED','POLLING','RECONCILED','REJECTED','UNKNOWN','BLOCKED')) OR
  (OLD.state='ACKNOWLEDGED' AND NEW.state IN ('POLLING','RECONCILED','REJECTED','UNKNOWN','BLOCKED')) OR
  (OLD.state='POLLING' AND NEW.state IN ('ACKNOWLEDGED','RECONCILED','REJECTED','UNKNOWN','BLOCKED')) OR
  (OLD.state='UNKNOWN' AND NEW.state IN ('POLLING','REJECTED')) OR
  (OLD.state='UNKNOWN' AND NEW.state='RECONCILED' AND EXISTS(SELECT 1 FROM orders_reconciliations r WHERE r.work_id=OLD.id))
 ) THEN RAISE EXCEPTION 'invalid order work transition' USING ERRCODE='55000'; END IF;
 IF NEW.state='DISPATCHING' AND NEW.dispatch_count<>1 THEN RAISE EXCEPTION 'order dispatch requires durable intent' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_work_guard BEFORE UPDATE OR DELETE ON orders_work FOR EACH ROW EXECUTE FUNCTION orders_work_guard();

-- +goose StatementBegin
CREATE FUNCTION orders_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.task_type<>'orders_sync' AND (TG_OP='DELETE' OR NEW.task_type<>'orders_sync') THEN IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW; END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'order outbox history is immutable' USING ERRCODE='55000'; END IF;
 IF (NEW.operation_key,NEW.task_type,NEW.orders_operation_id,NEW.orders_work_id,NEW.orders_tenant_id,NEW.orders_moysklad_account,NEW.orders_ozon_account,NEW.max_attempts) IS DISTINCT FROM (OLD.operation_key,OLD.task_type,OLD.orders_operation_id,OLD.orders_work_id,OLD.orders_tenant_id,OLD.orders_moysklad_account,OLD.orders_ozon_account,OLD.max_attempts) OR NEW.attempts<OLD.attempts THEN RAISE EXCEPTION 'order outbox identity is immutable' USING ERRCODE='55000'; END IF;
 IF OLD.state IN ('succeeded','failed','cancelled') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'terminal order task is immutable' USING ERRCODE='55000'; END IF;
 IF OLD.state='running' AND NEW.state='pending' AND NOT EXISTS(SELECT 1 FROM orders_work w WHERE w.id=OLD.orders_work_id AND (w.state IN ('ACKNOWLEDGED','POLLING','UNKNOWN') OR (w.state='QUEUED' AND w.dispatch_count=0))) THEN RAISE EXCEPTION 'only unsent or readback work may be rescheduled' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_outbox_guard BEFORE UPDATE OR DELETE ON outbox_tasks FOR EACH ROW EXECUTE FUNCTION orders_outbox_guard();

-- +goose StatementBegin
CREATE FUNCTION orders_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE wid text; ws text; qs text; wc integer;
BEGIN
 IF TG_TABLE_NAME='outbox_tasks' THEN IF NEW.task_type<>'orders_sync' THEN RETURN NULL; END IF; wid:=NEW.orders_work_id; ELSE wid:=NEW.id; END IF;
 SELECT w.state,w.dispatch_count,q.state INTO ws,wc,qs FROM orders_work w JOIN outbox_tasks q ON q.orders_work_id=w.id AND q.task_type='orders_sync' WHERE w.id=wid;
 IF NOT FOUND THEN RAISE EXCEPTION 'order work requires its outbox task' USING ERRCODE='23514'; END IF;
 IF ws='RECONCILED' AND (qs<>'succeeded' OR NOT EXISTS(SELECT 1 FROM orders_reconciliations r WHERE r.work_id=wid)) THEN RAISE EXCEPTION 'order reconciliation requires durable readback' USING ERRCODE='23514'; END IF;
 IF ws IN ('REJECTED','BLOCKED') AND qs<>'failed' THEN RAISE EXCEPTION 'order failure requires failed task' USING ERRCODE='23514'; END IF;
 IF ws IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN') AND qs NOT IN ('pending','running') THEN RAISE EXCEPTION 'order active work requires active task' USING ERRCODE='23514'; END IF;
 IF wc=1 AND (NOT EXISTS(SELECT 1 FROM orders_attempts a WHERE a.work_id=wid AND a.phase='DISPATCHING') OR NOT EXISTS(SELECT 1 FROM orders_write_intents i WHERE i.work_id=wid)) THEN RAISE EXCEPTION 'order dispatch requires exact immutable intent' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER orders_work_consistency AFTER INSERT OR UPDATE ON orders_work DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION orders_consistency();
CREATE CONSTRAINT TRIGGER orders_outbox_consistency AFTER INSERT OR UPDATE ON outbox_tasks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION orders_consistency();

CREATE TRIGGER orders_config_revisions_immutable BEFORE UPDATE OR DELETE ON orders_config_revisions FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_grants_immutable BEFORE UPDATE OR DELETE ON orders_automation_grants FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_commercial_orders_immutable BEFORE UPDATE OR DELETE ON orders_commercial_orders FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_posting_identities_immutable BEFORE UPDATE OR DELETE ON orders_posting_identities FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_posting_observations_immutable BEFORE UPDATE OR DELETE ON orders_posting_observations FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_posting_conflicts_immutable BEFORE UPDATE OR DELETE ON orders_posting_conflicts FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_posting_head_events_immutable BEFORE UPDATE OR DELETE ON orders_posting_head_events FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_line_identities_immutable BEFORE UPDATE OR DELETE ON orders_line_identities FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_secrets_no_update BEFORE UPDATE ON orders_posting_secrets FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_ingestion_pages_immutable BEFORE UPDATE OR DELETE ON orders_ingestion_pages FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_previews_immutable BEFORE UPDATE OR DELETE ON orders_previews FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_operations_immutable BEFORE UPDATE OR DELETE ON orders_operations FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_work_mappings_immutable BEFORE UPDATE OR DELETE ON orders_work_mappings FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_work_warehouses_immutable BEFORE UPDATE OR DELETE ON orders_work_warehouses FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_write_intents_immutable BEFORE UPDATE OR DELETE ON orders_write_intents FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_attempts_immutable BEFORE UPDATE OR DELETE ON orders_attempts FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_receipts_immutable BEFORE UPDATE OR DELETE ON orders_receipts FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_reconciliations_immutable BEFORE UPDATE OR DELETE ON orders_reconciliations FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_metadata_revisions_immutable BEFORE UPDATE OR DELETE ON orders_metadata_revisions FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_metadata_attributes_immutable BEFORE UPDATE OR DELETE ON orders_metadata_attributes FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER orders_metadata_creation_results_immutable BEFORE UPDATE OR DELETE ON orders_metadata_creation_results FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_configs) OR EXISTS(SELECT 1 FROM orders_posting_observations) OR EXISTS(SELECT 1 FROM orders_operations) OR EXISTS(SELECT 1 FROM orders_metadata_revisions) OR EXISTS(SELECT 1 FROM orders_metadata_creation_intents) OR EXISTS(SELECT 1 FROM outbox_tasks WHERE task_type='orders_sync') THEN RAISE EXCEPTION 'refusing to discard order synchronization evidence' USING ERRCODE='55000'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER orders_outbox_consistency ON outbox_tasks;
DROP TRIGGER orders_outbox_guard ON outbox_tasks;
ALTER TABLE outbox_tasks DROP CONSTRAINT orders_outbox_scope_fk, DROP CONSTRAINT orders_outbox_payload_check, DROP CONSTRAINT outbox_task_payload_check, DROP CONSTRAINT catalog_task_type_check;
DROP INDEX orders_outbox_work;
DROP INDEX orders_outbox_claim;
ALTER TABLE outbox_tasks DROP COLUMN orders_operation_id, DROP COLUMN orders_work_id, DROP COLUMN orders_tenant_id, DROP COLUMN orders_moysklad_account, DROP COLUMN orders_ozon_account;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK(task_type IN ('mock','ozon_catalog_import','moysklad_catalog_import','stock_publication','catalog_sync'));
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK(
 (task_type='mock' AND state<>'cancelled' AND observation_id IS NOT NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL)
 OR (task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND observation_id IS NULL AND import_operation_id IS NOT NULL AND import_tenant_id IS NOT NULL AND import_account_id IS NOT NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL)
 OR (task_type='stock_publication' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NOT NULL AND max_attempts=1 AND attempts<=1 AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL)
 OR (task_type='catalog_sync' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NOT NULL AND catalog_work_id IS NOT NULL AND catalog_tenant_id IS NOT NULL AND catalog_moysklad_account IS NOT NULL AND catalog_ozon_account IS NOT NULL AND max_attempts=1 AND attempts<=1));
DROP TABLE orders_metadata_creation_results,orders_metadata_creation_intents,orders_metadata_attributes,orders_metadata_revisions;
DROP TABLE orders_reconciliations,orders_receipts,orders_attempts,orders_write_intents,orders_work_warehouses,orders_work_mappings,orders_work,orders_operations,orders_previews,orders_projection_bindings;
DROP TABLE orders_posting_secrets,orders_ingestion_pages,orders_checkpoints,orders_ingestion_leases,orders_line_identities,orders_posting_conflicts,orders_posting_head_events,orders_posting_heads,orders_posting_observations,orders_posting_identities,orders_commercial_orders;
DROP TABLE orders_automation_grants;
ALTER TABLE orders_configs DROP CONSTRAINT orders_current_config_fk;
DROP TABLE orders_config_revisions,orders_configs;
DROP FUNCTION orders_consistency(),orders_outbox_guard(),orders_work_guard(),orders_binding_guard(),orders_config_guard(),orders_actor_guard(),orders_metadata_intent_guard();
ALTER TABLE saas_merchant_accounts DROP CONSTRAINT orders_merchant_owner_scope;
