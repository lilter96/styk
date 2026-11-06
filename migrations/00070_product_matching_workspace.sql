-- +goose Up
-- Unknown lifecycle in historic imports remains NULL and cannot auto-confirm.
ALTER TABLE source_entities ADD COLUMN archived boolean;
CREATE TABLE product_matching_policies (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 moysklad_provider text NOT NULL DEFAULT 'moysklad' CHECK(moysklad_provider='moysklad'),
 ozon_provider text NOT NULL DEFAULT 'ozon' CHECK(ozon_provider='ozon'),
 revision bigint NOT NULL CHECK(revision>0),
 policy jsonb NOT NULL,
 actor text NOT NULL,
 owner_user_id text NOT NULL DEFAULT '',
 activated_moy_import bigint NOT NULL DEFAULT 0,
 activated_ozon_import bigint NOT NULL DEFAULT 0,
 last_checked_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_provider,moysklad_account) REFERENCES stock_connections(tenant_id,source,account_id),
 FOREIGN KEY(tenant_id,ozon_provider,ozon_account) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE INDEX product_matching_policies_due ON product_matching_policies(last_checked_at)
 WHERE policy->>'after_import'='true' AND policy->>'auto_confirm'='true';
CREATE TABLE product_matching_policy_audits (
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 revision bigint NOT NULL, policy jsonb NOT NULL, actor text NOT NULL, owner_user_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,revision),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES product_matching_policies(tenant_id,moysklad_account,ozon_account)
);
CREATE TRIGGER product_matching_policy_audits_immutable BEFORE UPDATE OR DELETE ON product_matching_policy_audits FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TABLE product_matching_previews (
 id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 actor text NOT NULL,
 preview jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account)
);
CREATE TABLE product_matching_runs (
 id text PRIMARY KEY,
 preview_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 actor text NOT NULL,
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
 request_hash text NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,moysklad_account,ozon_account,idempotency_key),
 FOREIGN KEY(preview_id,tenant_id,moysklad_account,ozon_account) REFERENCES product_matching_previews(id,tenant_id,moysklad_account,ozon_account)
);
CREATE INDEX product_matching_runs_scope ON product_matching_runs(tenant_id,moysklad_account,ozon_account,created_at DESC,id);
CREATE TRIGGER product_matching_previews_immutable BEFORE UPDATE OR DELETE ON product_matching_previews FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER product_matching_runs_immutable BEFORE UPDATE OR DELETE ON product_matching_runs FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

CREATE TABLE product_matching_cycles (
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 revision bigint NOT NULL, moy_import bigint NOT NULL REFERENCES source_imports(id), ozon_import bigint NOT NULL REFERENCES source_imports(id),
 run_id text NOT NULL REFERENCES product_matching_runs(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,revision,moy_import,ozon_import),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,revision) REFERENCES product_matching_policy_audits(tenant_id,moysklad_account,ozon_account,revision)
);
CREATE TRIGGER product_matching_cycles_immutable BEFORE UPDATE OR DELETE ON product_matching_cycles FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

CREATE TABLE catalog_created_links (
 work_id text PRIMARY KEY REFERENCES catalog_sync_work(id),
 state text NOT NULL CHECK(state IN ('WAITING','LINKED','CONFLICT')),
 mapping_id bigint REFERENCES product_mappings(id),
 checked_at timestamptz NOT NULL,
 CHECK((state='LINKED')=(mapping_id IS NOT NULL))
);
CREATE INDEX catalog_created_links_pending ON catalog_created_links(checked_at,work_id) WHERE state='WAITING';

-- +goose Down
-- Used policy and decision audit must survive a rollback; use a forward fix.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM catalog_created_links) OR EXISTS(SELECT 1 FROM product_matching_policies) OR EXISTS(SELECT 1 FROM product_matching_previews) OR EXISTS(SELECT 1 FROM product_matching_runs) THEN
  RAISE EXCEPTION 'Product matching audit exists; retain migration 70 and use a forward fix';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE catalog_created_links;
DROP TABLE product_matching_cycles;
DROP TABLE product_matching_runs;
DROP TABLE product_matching_previews;
DROP TABLE product_matching_policy_audits;
DROP TABLE product_matching_policies;
ALTER TABLE source_entities DROP COLUMN archived;
