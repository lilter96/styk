-- +goose Up
-- Existing imports are not assigned to tenants by this migration.
CREATE TABLE stock_connections (
 tenant_id text NOT NULL CHECK(length(trim(tenant_id)) > 0 AND tenant_id=trim(tenant_id)),
 source text NOT NULL CHECK(source IN ('moysklad','ozon')),
 account_id text NOT NULL CHECK(length(trim(account_id)) > 0 AND account_id=trim(account_id)),
 provenance text NOT NULL CHECK(length(trim(provenance)) > 0),
 include_import_history boolean NOT NULL DEFAULT false,
 registered_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,source,account_id),
 UNIQUE(source,account_id)
);

CREATE TABLE stock_pools (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id text NOT NULL,
 source text NOT NULL DEFAULT 'moysklad' CHECK(source='moysklad'),
 account_id text NOT NULL,
 warehouse_id text NOT NULL CHECK(length(trim(warehouse_id)) > 0 AND warehouse_id=trim(warehouse_id)),
 item_kind text NOT NULL CHECK(item_kind IN ('product','variant')),
 item_id text NOT NULL CHECK(length(trim(item_id)) > 0 AND item_id=trim(item_id)),
 FOREIGN KEY(tenant_id,source,account_id) REFERENCES stock_connections(tenant_id,source,account_id),
 UNIQUE(source,account_id,warehouse_id,item_kind,item_id),
 UNIQUE(id,tenant_id,account_id,warehouse_id,item_kind,item_id)
);

CREATE TABLE stock_policies (
 tenant_id text NOT NULL CHECK(length(trim(tenant_id)) > 0 AND tenant_id=trim(tenant_id)),
 version integer NOT NULL CHECK(version > 0),
 unit text NOT NULL,
 safety_stock text NOT NULL,
 max_observation_age_ns bigint NOT NULL CHECK(max_observation_age_ns > 0),
 decision_lifetime_ns bigint NOT NULL CHECK(decision_lifetime_ns > 0),
 PRIMARY KEY(tenant_id,version)
);

ALTER TABLE source_imports ADD CONSTRAINT stock_import_scope UNIQUE(id,source,account_id);
ALTER TABLE product_mapping_history ADD CONSTRAINT stock_product_mapping_scope UNIQUE
 (mapping_id,version,tenant_id,ozon_account,moysklad_account,target_kind,target_id,ozon_product_id,ozon_offer_id);
ALTER TABLE warehouse_mapping_history ADD CONSTRAINT stock_warehouse_mapping_scope UNIQUE
 (mapping_id,version,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id);
CREATE INDEX stock_shared_product_idx ON product_mappings(moysklad_account,target_kind,target_id);
CREATE INDEX stock_shared_source_warehouse_idx ON warehouse_mappings(moysklad_account,moysklad_warehouse_id);
CREATE INDEX stock_shared_ozon_warehouse_idx ON warehouse_mappings(ozon_account,ozon_warehouse_id);

CREATE TABLE stock_decisions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id text NOT NULL,
 pool_id bigint NOT NULL,
 moysklad_account text NOT NULL,
 moysklad_warehouse_id text NOT NULL,
 item_kind text NOT NULL,
 item_id text NOT NULL,
 ozon_source text NOT NULL DEFAULT 'ozon' CHECK(ozon_source='ozon'),
 ozon_account text NOT NULL,
 ozon_warehouse_id text NOT NULL CHECK(length(trim(ozon_warehouse_id)) > 0 AND ozon_warehouse_id=trim(ozon_warehouse_id)),
 ozon_product_id text NOT NULL CHECK(length(trim(ozon_product_id)) > 0 AND ozon_product_id=trim(ozon_product_id)),
 offer_id text,
 product_mapping_id bigint,
 product_mapping_version integer,
 warehouse_mapping_id bigint,
 warehouse_mapping_version integer,
 policy_version integer NOT NULL CHECK(policy_version > 0),
 evaluated_at timestamptz NOT NULL,
 expires_at timestamptz,
 -- No durable proof source exists yet; synthetic READY is exercised only in the pure calculator.
 status text NOT NULL CHECK(status IN ('UNKNOWN','BLOCKED')),
 quantity numeric,
 decision jsonb NOT NULL CHECK(jsonb_typeof(decision)='object'),
 CHECK (quantity IS NULL AND expires_at IS NULL),
 CHECK ((product_mapping_id IS NULL AND product_mapping_version IS NULL AND offer_id IS NULL)
     OR (product_mapping_id IS NOT NULL AND product_mapping_version IS NOT NULL AND offer_id IS NOT NULL)),
 CHECK ((warehouse_mapping_id IS NULL AND warehouse_mapping_version IS NULL)
     OR (warehouse_mapping_id IS NOT NULL AND warehouse_mapping_version IS NOT NULL)),
 FOREIGN KEY(pool_id,tenant_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id)
   REFERENCES stock_pools(id,tenant_id,account_id,warehouse_id,item_kind,item_id),
 FOREIGN KEY(tenant_id,ozon_source,ozon_account) REFERENCES stock_connections(tenant_id,source,account_id),
 FOREIGN KEY(tenant_id,policy_version) REFERENCES stock_policies(tenant_id,version),
 FOREIGN KEY(product_mapping_id,product_mapping_version,tenant_id,ozon_account,moysklad_account,item_kind,item_id,ozon_product_id,offer_id)
   REFERENCES product_mapping_history(mapping_id,version,tenant_id,ozon_account,moysklad_account,target_kind,target_id,ozon_product_id,ozon_offer_id),
 FOREIGN KEY(warehouse_mapping_id,warehouse_mapping_version,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id)
   REFERENCES warehouse_mapping_history(mapping_id,version,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id),
 UNIQUE(id,tenant_id,pool_id,ozon_account,ozon_warehouse_id,ozon_product_id)
);
CREATE INDEX stock_decisions_pool_idx ON stock_decisions(tenant_id,pool_id,id DESC);

-- One immutable evidence record is required even when no source row exists.
CREATE TABLE stock_decision_evidence (
 decision_id bigint PRIMARY KEY,
 tenant_id text NOT NULL,
 pool_id bigint NOT NULL,
 moysklad_source text NOT NULL DEFAULT 'moysklad' CHECK(moysklad_source='moysklad'),
 moysklad_account text NOT NULL,
 moysklad_warehouse_id text NOT NULL,
 item_kind text NOT NULL,
 item_id text NOT NULL,
 ozon_source text NOT NULL DEFAULT 'ozon' CHECK(ozon_source='ozon'),
 ozon_account text NOT NULL,
 ozon_warehouse_id text NOT NULL,
 ozon_product_id text NOT NULL,
 moysklad_import_id bigint,
 moysklad_stock_import_id bigint,
 ozon_import_id bigint,
 ozon_stock_import_id bigint,
 posting_batch_id bigint,
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 CHECK(moysklad_stock_import_id IS NULL OR (moysklad_import_id IS NOT NULL AND moysklad_stock_import_id=moysklad_import_id)),
 CHECK(ozon_stock_import_id IS NULL OR (ozon_import_id IS NOT NULL AND ozon_stock_import_id=ozon_import_id)),
 FOREIGN KEY(decision_id,tenant_id,pool_id,ozon_account,ozon_warehouse_id,ozon_product_id)
   REFERENCES stock_decisions(id,tenant_id,pool_id,ozon_account,ozon_warehouse_id,ozon_product_id),
 FOREIGN KEY(pool_id,tenant_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id)
   REFERENCES stock_pools(id,tenant_id,account_id,warehouse_id,item_kind,item_id),
 FOREIGN KEY(moysklad_import_id,moysklad_source,moysklad_account) REFERENCES source_imports(id,source,account_id),
 FOREIGN KEY(moysklad_stock_import_id,item_kind,item_id,moysklad_warehouse_id)
   REFERENCES source_warehouse_stock_observations(import_id,kind,external_id,warehouse_id),
 FOREIGN KEY(ozon_import_id,ozon_source,ozon_account) REFERENCES source_imports(id,source,account_id),
 FOREIGN KEY(ozon_stock_import_id,ozon_product_id,ozon_warehouse_id)
   REFERENCES ozon_fbs_stock_observations(import_id,product_id,warehouse_id),
 FOREIGN KEY(posting_batch_id,tenant_id,ozon_account) REFERENCES ozon_posting_batches(id,tenant_id,account_id)
);

-- +goose StatementBegin
CREATE FUNCTION stock_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'stock ownership, pools and decisions are immutable' USING ERRCODE='55000';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER stock_connections_immutable BEFORE UPDATE OR DELETE ON stock_connections FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER stock_pools_immutable BEFORE UPDATE OR DELETE ON stock_pools FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER stock_policies_immutable BEFORE UPDATE OR DELETE ON stock_policies FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER stock_decisions_immutable BEFORE UPDATE OR DELETE ON stock_decisions FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER stock_evidence_immutable BEFORE UPDATE OR DELETE ON stock_decision_evidence FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose StatementBegin
CREATE FUNCTION stock_require_decision_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM stock_decision_evidence WHERE decision_id=NEW.id) THEN
  RAISE EXCEPTION 'stock decision requires evidence' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER stock_decision_evidence_required AFTER INSERT ON stock_decisions
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stock_require_decision_evidence();

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM stock_connections) OR EXISTS (SELECT 1 FROM stock_policies) THEN
  RAISE EXCEPTION 'refusing to drop stock ownership, policy, or decision history';
 END IF;
END;
$$;
-- +goose StatementEnd
DROP TABLE stock_decision_evidence;
DROP TABLE stock_decisions;
DROP TABLE stock_policies;
DROP TABLE stock_pools;
DROP TABLE stock_connections;
DROP FUNCTION stock_require_decision_evidence();
DROP FUNCTION stock_reject_mutation();
ALTER TABLE warehouse_mapping_history DROP CONSTRAINT stock_warehouse_mapping_scope;
ALTER TABLE product_mapping_history DROP CONSTRAINT stock_product_mapping_scope;
ALTER TABLE source_imports DROP CONSTRAINT stock_import_scope;
DROP INDEX stock_shared_product_idx;
DROP INDEX stock_shared_source_warehouse_idx;
DROP INDEX stock_shared_ozon_warehouse_idx;
