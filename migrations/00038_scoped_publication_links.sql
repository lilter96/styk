-- +goose Up
-- Historical rows remain intact; all new and updated links must agree with
-- the tenant and both account identities of the owning operation.
ALTER TABLE stock_sync_reviews ADD CONSTRAINT saas_stock_review_scope_unique
 UNIQUE(id,tenant_id,moysklad_account,ozon_account);
ALTER TABLE stock_publications ADD CONSTRAINT saas_publication_scope_unique
 UNIQUE(id,tenant_id,moysklad_account,ozon_account);
ALTER TABLE stock_publications ADD CONSTRAINT saas_publication_ozon_unique
 UNIQUE(id,ozon_account);

ALTER TABLE stock_sync_settings ADD CONSTRAINT saas_settings_review_scope_fk
 FOREIGN KEY(review_id,tenant_id,moysklad_account,ozon_account)
 REFERENCES stock_sync_reviews(id,tenant_id,moysklad_account,ozon_account) NOT VALID;
ALTER TABLE stock_publications ADD CONSTRAINT saas_publication_review_scope_fk
 FOREIGN KEY(review_id,tenant_id,moysklad_account,ozon_account)
 REFERENCES stock_sync_reviews(id,tenant_id,moysklad_account,ozon_account) NOT VALID;
ALTER TABLE stock_publication_targets ADD CONSTRAINT saas_publication_target_account_fk
 FOREIGN KEY(operation_id,ozon_account) REFERENCES stock_publications(id,ozon_account) NOT VALID;
ALTER TABLE stock_sync_mutations ADD CONSTRAINT saas_sync_mutation_publication_scope_fk
 FOREIGN KEY(operation_id,tenant_id,moysklad_account,ozon_account)
 REFERENCES stock_publications(id,tenant_id,moysklad_account,ozon_account) NOT VALID;
ALTER TABLE stock_initial_publications ADD CONSTRAINT saas_initial_publication_scope_fk
 FOREIGN KEY(publication_id,tenant_id,moysklad_account,ozon_account)
 REFERENCES stock_publications(id,tenant_id,moysklad_account,ozon_account) NOT VALID;

ALTER TABLE stock_auto_cycles ADD COLUMN saas_moy_provider text GENERATED ALWAYS AS ('moysklad'::text) STORED;
ALTER TABLE stock_auto_cycles ADD COLUMN saas_ozon_provider text GENERATED ALWAYS AS ('ozon'::text) STORED;
ALTER TABLE stock_auto_cycles ADD CONSTRAINT saas_cycle_moy_import_scope_fk
 FOREIGN KEY(moysklad_import_id,tenant_id,saas_moy_provider,moysklad_account)
 REFERENCES ozon_import_operations(id,tenant_id,provider,account_id) NOT VALID;
ALTER TABLE stock_auto_cycles ADD CONSTRAINT saas_cycle_ozon_import_scope_fk
 FOREIGN KEY(ozon_import_id,tenant_id,saas_ozon_provider,ozon_account)
 REFERENCES ozon_import_operations(id,tenant_id,provider,account_id) NOT VALID;
ALTER TABLE stock_auto_cycles ADD CONSTRAINT saas_cycle_publication_scope_fk
 FOREIGN KEY(publication_id,tenant_id,moysklad_account,ozon_account)
 REFERENCES stock_publications(id,tenant_id,moysklad_account,ozon_account) NOT VALID;

-- +goose StatementBegin
CREATE FUNCTION saas_cycle_target_scope_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM stock_auto_cycles c
  JOIN stock_publications p ON p.id=NEW.publication_id
   AND p.tenant_id=c.tenant_id AND p.moysklad_account=c.moysklad_account AND p.ozon_account=c.ozon_account
  JOIN stock_publication_targets t ON t.operation_id=p.id AND t.ozon_account=p.ozon_account
   AND t.product_id=NEW.product_id AND t.warehouse_id=NEW.warehouse_id
  WHERE c.id=NEW.cycle_id
 ) THEN RAISE EXCEPTION 'automatic cycle publication is outside its tenant or target scope' USING ERRCODE='23503'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER saas_cycle_target_scope_guard BEFORE INSERT ON stock_auto_cycle_publications
 FOR EACH ROW EXECUTE FUNCTION saas_cycle_target_scope_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM saas_users) THEN
  RAISE EXCEPTION 'refusing to remove publication scope guards while merchant identities exist';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER saas_cycle_target_scope_guard ON stock_auto_cycle_publications;
DROP FUNCTION saas_cycle_target_scope_guard();
ALTER TABLE stock_auto_cycles DROP CONSTRAINT saas_cycle_publication_scope_fk;
ALTER TABLE stock_auto_cycles DROP CONSTRAINT saas_cycle_ozon_import_scope_fk;
ALTER TABLE stock_auto_cycles DROP CONSTRAINT saas_cycle_moy_import_scope_fk;
ALTER TABLE stock_auto_cycles DROP COLUMN saas_ozon_provider, DROP COLUMN saas_moy_provider;
ALTER TABLE stock_initial_publications DROP CONSTRAINT saas_initial_publication_scope_fk;
ALTER TABLE stock_sync_mutations DROP CONSTRAINT saas_sync_mutation_publication_scope_fk;
ALTER TABLE stock_publication_targets DROP CONSTRAINT saas_publication_target_account_fk;
ALTER TABLE stock_publications DROP CONSTRAINT saas_publication_review_scope_fk;
ALTER TABLE stock_sync_settings DROP CONSTRAINT saas_settings_review_scope_fk;
ALTER TABLE stock_publications DROP CONSTRAINT saas_publication_ozon_unique;
ALTER TABLE stock_publications DROP CONSTRAINT saas_publication_scope_unique;
ALTER TABLE stock_sync_reviews DROP CONSTRAINT saas_stock_review_scope_unique;
