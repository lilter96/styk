-- +goose Up
CREATE TABLE orders_source_permissions (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 enabled boolean NOT NULL,
 owner_user_id text NOT NULL,
 actor text NOT NULL,
 consented_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES orders_configs(tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES saas_merchant_accounts(tenant_id,user_id)
);
CREATE TABLE orders_source_refreshes (
 import_id text PRIMARY KEY REFERENCES ozon_import_operations(id),
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '10 minutes',
 state text NOT NULL DEFAULT 'QUEUED' CHECK(state IN('QUEUED','READING','SUCCEEDED','FAILED','PARTIAL','CANCELLED')),
 code text NOT NULL DEFAULT '',
 postings_count bigint NOT NULL DEFAULT 0 CHECK(postings_count>=0),
 finished_at timestamptz,
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES orders_configs(tenant_id,moysklad_account,ozon_account)
);
CREATE INDEX orders_source_refreshes_pending ON orders_source_refreshes(tenant_id,moysklad_account,ozon_account) WHERE state IN('QUEUED','READING');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_source_refreshes) OR EXISTS(SELECT 1 FROM orders_source_permissions) THEN
  RAISE EXCEPTION 'Cannot discard recorded order source permissions or refresh history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_source_refreshes;
DROP TABLE orders_source_permissions;
