-- +goose Up
-- A disabled draft can be saved for any registered account pair. The existing
-- enabled publication exception remains fixed to the owner's test accounts.
ALTER TABLE stock_auto_configs DROP CONSTRAINT stock_auto_configs_check;
ALTER TABLE stock_auto_configs ADD CONSTRAINT stock_auto_configs_check CHECK(
 NOT enabled OR (tenant_id='cross-e2e' AND moysklad_account='2928cf35-d30a-11f1-0a80-1b9000000355' AND ozon_account='1549477')
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_auto_configs WHERE tenant_id<>'cross-e2e' OR moysklad_account<>'2928cf35-d30a-11f1-0a80-1b9000000355' OR ozon_account<>'1549477') THEN
  RAISE EXCEPTION 'refusing to discard merchant synchronization drafts';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE stock_auto_configs DROP CONSTRAINT stock_auto_configs_check;
ALTER TABLE stock_auto_configs ADD CONSTRAINT stock_auto_configs_check CHECK(
 tenant_id='cross-e2e' AND moysklad_account='2928cf35-d30a-11f1-0a80-1b9000000355' AND ozon_account='1549477'
);
