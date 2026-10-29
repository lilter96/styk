-- +goose Up
-- Legacy read-only posting imports predate provider registration. Newly active
-- merchant workspaces must always bind those records to their own account.
-- +goose StatementBegin
CREATE FUNCTION saas_posting_owner_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM saas_workspaces WHERE id=NEW.tenant_id AND NOT legacy_unclaimed)
 AND NOT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=NEW.tenant_id AND source='ozon' AND account_id=NEW.account_id)
 THEN RAISE EXCEPTION 'posting account is outside the merchant workspace' USING ERRCODE='23503'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER saas_posting_batch_owner BEFORE INSERT OR UPDATE ON ozon_posting_batches FOR EACH ROW EXECUTE FUNCTION saas_posting_owner_guard();
CREATE TRIGGER saas_posting_identity_owner BEFORE INSERT OR UPDATE ON ozon_posting_identities FOR EACH ROW EXECUTE FUNCTION saas_posting_owner_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM saas_users) THEN
  RAISE EXCEPTION 'refusing to remove merchant posting isolation while merchant identities exist';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER saas_posting_identity_owner ON ozon_posting_identities;
DROP TRIGGER saas_posting_batch_owner ON ozon_posting_batches;
DROP FUNCTION saas_posting_owner_guard();
