-- +goose Up
-- NOT VALID preserves historical rows while enforcing every new reference.
-- Existing mismatches must be reviewed before a legacy workspace is claimed.
ALTER TABLE stock_connections ADD CONSTRAINT stock_connection_workspace_fk
 FOREIGN KEY(tenant_id) REFERENCES saas_workspaces(id) NOT VALID;

ALTER TABLE product_mappings ADD COLUMN tenant_ozon_source text GENERATED ALWAYS AS ('ozon'::text) STORED;
ALTER TABLE product_mappings ADD COLUMN tenant_moy_source text GENERATED ALWAYS AS ('moysklad'::text) STORED;
ALTER TABLE product_mappings ADD CONSTRAINT product_mapping_ozon_owner_fk
 FOREIGN KEY(tenant_id,tenant_ozon_source,ozon_account) REFERENCES stock_connections(tenant_id,source,account_id) NOT VALID;
ALTER TABLE product_mappings ADD CONSTRAINT product_mapping_moy_owner_fk
 FOREIGN KEY(tenant_id,tenant_moy_source,moysklad_account) REFERENCES stock_connections(tenant_id,source,account_id) NOT VALID;

ALTER TABLE warehouse_mappings ADD COLUMN tenant_ozon_source text GENERATED ALWAYS AS ('ozon'::text) STORED;
ALTER TABLE warehouse_mappings ADD COLUMN tenant_moy_source text GENERATED ALWAYS AS ('moysklad'::text) STORED;
ALTER TABLE warehouse_mappings ADD CONSTRAINT warehouse_mapping_ozon_owner_fk
 FOREIGN KEY(tenant_id,tenant_ozon_source,ozon_account) REFERENCES stock_connections(tenant_id,source,account_id) NOT VALID;
ALTER TABLE warehouse_mappings ADD CONSTRAINT warehouse_mapping_moy_owner_fk
 FOREIGN KEY(tenant_id,tenant_moy_source,moysklad_account) REFERENCES stock_connections(tenant_id,source,account_id) NOT VALID;

-- +goose StatementBegin
CREATE FUNCTION saas_import_outbox_identity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (OLD.task_type IN ('ozon_catalog_import','moysklad_catalog_import') OR NEW.task_type IN ('ozon_catalog_import','moysklad_catalog_import'))
 AND (NEW.operation_key,NEW.task_type,NEW.import_operation_id,NEW.import_tenant_id,NEW.import_account_id,NEW.max_attempts)
 IS DISTINCT FROM (OLD.operation_key,OLD.task_type,OLD.import_operation_id,OLD.import_tenant_id,OLD.import_account_id,OLD.max_attempts)
 THEN RAISE EXCEPTION 'import outbox identity is immutable' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER saas_import_outbox_identity_guard BEFORE UPDATE ON outbox_tasks FOR EACH ROW EXECUTE FUNCTION saas_import_outbox_identity_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM saas_users) THEN
  RAISE EXCEPTION 'refusing to remove tenant reference guards while merchant identities exist';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER saas_import_outbox_identity_guard ON outbox_tasks;
DROP FUNCTION saas_import_outbox_identity_guard();
ALTER TABLE warehouse_mappings DROP CONSTRAINT warehouse_mapping_moy_owner_fk;
ALTER TABLE warehouse_mappings DROP CONSTRAINT warehouse_mapping_ozon_owner_fk;
ALTER TABLE warehouse_mappings DROP COLUMN tenant_moy_source, DROP COLUMN tenant_ozon_source;
ALTER TABLE product_mappings DROP CONSTRAINT product_mapping_moy_owner_fk;
ALTER TABLE product_mappings DROP CONSTRAINT product_mapping_ozon_owner_fk;
ALTER TABLE product_mappings DROP COLUMN tenant_moy_source, DROP COLUMN tenant_ozon_source;
ALTER TABLE stock_connections DROP CONSTRAINT stock_connection_workspace_fk;
