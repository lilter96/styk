-- +goose Up
ALTER TABLE product_mapping_reviews DROP CONSTRAINT product_mapping_reviews_principal_type_check;
ALTER TABLE product_mapping_reviews ADD CONSTRAINT product_mapping_reviews_principal_type_check CHECK(principal_type IN ('LOCAL_OPERATOR_SESSION','MERCHANT_SESSION'));
ALTER TABLE warehouse_mapping_reviews DROP CONSTRAINT warehouse_mapping_reviews_principal_type_check;
ALTER TABLE warehouse_mapping_reviews ADD CONSTRAINT warehouse_mapping_reviews_principal_type_check CHECK(principal_type IN ('LOCAL_OPERATOR_SESSION','MERCHANT_SESSION'));
ALTER TABLE product_mapping_confirmation_audits DROP CONSTRAINT product_mapping_confirmation_audits_principal_type_check;
ALTER TABLE product_mapping_confirmation_audits ADD CONSTRAINT product_mapping_confirmation_audits_principal_type_check CHECK(principal_type IN ('LOCAL_OPERATOR_SESSION','MERCHANT_SESSION'));
ALTER TABLE warehouse_mapping_confirmation_audits DROP CONSTRAINT warehouse_mapping_confirmation_audits_principal_type_check;
ALTER TABLE warehouse_mapping_confirmation_audits ADD CONSTRAINT warehouse_mapping_confirmation_audits_principal_type_check CHECK(principal_type IN ('LOCAL_OPERATOR_SESSION','MERCHANT_SESSION'));
ALTER TABLE stock_auto_config_audits DROP CONSTRAINT stock_auto_config_audits_principal_type_check;
ALTER TABLE stock_auto_config_audits ADD CONSTRAINT stock_auto_config_audits_principal_type_check CHECK(principal_type IN ('LOCAL_OPERATOR_SESSION','MERCHANT_SESSION'));
ALTER TABLE stock_publication_recoveries DROP CONSTRAINT stock_publication_recoveries_principal_type_check;
ALTER TABLE stock_publication_recoveries ADD CONSTRAINT stock_publication_recoveries_principal_type_check CHECK(principal_type IN ('LOCAL_OPERATOR_SESSION','MERCHANT_SESSION'));
ALTER TABLE ozon_import_operations DROP CONSTRAINT ozon_import_operations_principal_type_check;
ALTER TABLE ozon_import_operations ADD CONSTRAINT ozon_import_operations_principal_type_check CHECK(principal_type IN ('LOCAL_OPERATOR_SESSION','MERCHANT_SESSION','AUTOMATIC_POLICY'));
ALTER TABLE credential_save_requests ADD COLUMN actor text NOT NULL DEFAULT 'LEGACY_UNATTRIBUTED' CHECK(length(actor) BETWEEN 1 AND 256);

-- Hold membership, session and user row locks only for the local database
-- transaction, never over provider I/O. Revocation serializes with the commit.
-- +goose StatementBegin
CREATE FUNCTION saas_require_actor(p_tenant text,p_session text,p_admin boolean) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM saas_sessions s
 JOIN saas_users u ON u.id=s.user_id
 JOIN saas_memberships m ON m.user_id=u.id AND m.workspace_id=p_tenant
 JOIN saas_workspaces w ON w.id=m.workspace_id
 WHERE s.audit_id=p_session AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
   AND s.user_auth_version=u.auth_version AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL
   AND m.revoked_at IS NULL AND NOT w.legacy_unclaimed
   AND (m.role IN ('OWNER','ADMIN') OR (NOT p_admin AND m.role='OPERATOR'))
 FOR SHARE OF s,u,m;
 IF NOT FOUND THEN RAISE EXCEPTION 'merchant actor is no longer authorized' USING ERRCODE='42501'; END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION saas_validate_audit_actor() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.principal_type='MERCHANT_SESSION' THEN
  PERFORM saas_require_actor(NEW.tenant_id,NEW.session_audit_id,TG_TABLE_NAME IN ('stock_auto_config_audits','stock_publication_recoveries'));
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER saas_actor_product_review BEFORE INSERT ON product_mapping_reviews FOR EACH ROW EXECUTE FUNCTION saas_validate_audit_actor();
CREATE TRIGGER saas_actor_warehouse_review BEFORE INSERT ON warehouse_mapping_reviews FOR EACH ROW EXECUTE FUNCTION saas_validate_audit_actor();
CREATE TRIGGER saas_actor_product_confirmation BEFORE INSERT ON product_mapping_confirmation_audits FOR EACH ROW EXECUTE FUNCTION saas_validate_audit_actor();
CREATE TRIGGER saas_actor_warehouse_confirmation BEFORE INSERT ON warehouse_mapping_confirmation_audits FOR EACH ROW EXECUTE FUNCTION saas_validate_audit_actor();
CREATE TRIGGER saas_actor_automatic_policy BEFORE INSERT ON stock_auto_config_audits FOR EACH ROW EXECUTE FUNCTION saas_validate_audit_actor();
CREATE TRIGGER saas_actor_recovery BEFORE INSERT ON stock_publication_recoveries FOR EACH ROW EXECUTE FUNCTION saas_validate_audit_actor();
CREATE TRIGGER saas_actor_import BEFORE INSERT ON ozon_import_operations FOR EACH ROW EXECUTE FUNCTION saas_validate_audit_actor();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_mapping_reviews WHERE principal_type='MERCHANT_SESSION')
 OR EXISTS(SELECT 1 FROM warehouse_mapping_reviews WHERE principal_type='MERCHANT_SESSION')
 OR EXISTS(SELECT 1 FROM product_mapping_confirmation_audits WHERE principal_type='MERCHANT_SESSION')
 OR EXISTS(SELECT 1 FROM warehouse_mapping_confirmation_audits WHERE principal_type='MERCHANT_SESSION')
 OR EXISTS(SELECT 1 FROM stock_auto_config_audits WHERE principal_type='MERCHANT_SESSION')
 OR EXISTS(SELECT 1 FROM stock_publication_recoveries WHERE principal_type='MERCHANT_SESSION')
 OR EXISTS(SELECT 1 FROM ozon_import_operations WHERE principal_type='MERCHANT_SESSION')
 OR EXISTS(SELECT 1 FROM credential_save_requests WHERE actor LIKE 'MERCHANT_SESSION:%') THEN
  RAISE EXCEPTION 'refusing to discard merchant audit provenance';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER saas_actor_product_review ON product_mapping_reviews;
DROP TRIGGER saas_actor_warehouse_review ON warehouse_mapping_reviews;
DROP TRIGGER saas_actor_product_confirmation ON product_mapping_confirmation_audits;
DROP TRIGGER saas_actor_warehouse_confirmation ON warehouse_mapping_confirmation_audits;
DROP TRIGGER saas_actor_automatic_policy ON stock_auto_config_audits;
DROP TRIGGER saas_actor_recovery ON stock_publication_recoveries;
DROP TRIGGER saas_actor_import ON ozon_import_operations;
DROP FUNCTION saas_validate_audit_actor();
DROP FUNCTION saas_require_actor(text,text,boolean);
ALTER TABLE credential_save_requests DROP COLUMN actor;
ALTER TABLE product_mapping_reviews DROP CONSTRAINT product_mapping_reviews_principal_type_check;
ALTER TABLE product_mapping_reviews ADD CONSTRAINT product_mapping_reviews_principal_type_check CHECK(principal_type='LOCAL_OPERATOR_SESSION');
ALTER TABLE warehouse_mapping_reviews DROP CONSTRAINT warehouse_mapping_reviews_principal_type_check;
ALTER TABLE warehouse_mapping_reviews ADD CONSTRAINT warehouse_mapping_reviews_principal_type_check CHECK(principal_type='LOCAL_OPERATOR_SESSION');
ALTER TABLE product_mapping_confirmation_audits DROP CONSTRAINT product_mapping_confirmation_audits_principal_type_check;
ALTER TABLE product_mapping_confirmation_audits ADD CONSTRAINT product_mapping_confirmation_audits_principal_type_check CHECK(principal_type='LOCAL_OPERATOR_SESSION');
ALTER TABLE warehouse_mapping_confirmation_audits DROP CONSTRAINT warehouse_mapping_confirmation_audits_principal_type_check;
ALTER TABLE warehouse_mapping_confirmation_audits ADD CONSTRAINT warehouse_mapping_confirmation_audits_principal_type_check CHECK(principal_type='LOCAL_OPERATOR_SESSION');
ALTER TABLE stock_auto_config_audits DROP CONSTRAINT stock_auto_config_audits_principal_type_check;
ALTER TABLE stock_auto_config_audits ADD CONSTRAINT stock_auto_config_audits_principal_type_check CHECK(principal_type='LOCAL_OPERATOR_SESSION');
ALTER TABLE stock_publication_recoveries DROP CONSTRAINT stock_publication_recoveries_principal_type_check;
ALTER TABLE stock_publication_recoveries ADD CONSTRAINT stock_publication_recoveries_principal_type_check CHECK(principal_type='LOCAL_OPERATOR_SESSION');
ALTER TABLE ozon_import_operations DROP CONSTRAINT ozon_import_operations_principal_type_check;
ALTER TABLE ozon_import_operations ADD CONSTRAINT ozon_import_operations_principal_type_check CHECK(principal_type IN ('LOCAL_OPERATOR_SESSION','AUTOMATIC_POLICY'));
