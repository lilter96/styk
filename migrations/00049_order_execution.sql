-- +goose Up
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('mirror_create','mirror_update','posting_pack','posting_ship','posting_cancel','posting_deliver',
  'posting_split','posting_partial_cancel','posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create'));

CREATE TABLE orders_action_secrets (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 action text NOT NULL,
 reference text NOT NULL CHECK(reference ~ '^[0-9a-f]{64}$'),
 config_revision bigint NOT NULL CHECK(config_revision>0),
 ciphertext bytea NOT NULL CHECK(octet_length(ciphertext) BETWEEN 29 AND 132096),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>created_at AND expires_at<=created_at+interval '1 day'),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,posting_number,action,reference,config_revision),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision)
  REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision)
);
CREATE INDEX orders_action_secrets_expiry ON orders_action_secrets(expires_at);
CREATE TRIGGER orders_action_secrets_no_update BEFORE UPDATE ON orders_action_secrets
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_action_secrets) OR EXISTS(SELECT 1 FROM orders_work WHERE action NOT IN
 ('mirror_create','mirror_update','posting_pack','posting_ship','posting_cancel','posting_deliver')) THEN
  RAISE EXCEPTION 'order execution history prevents rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_action_secrets;
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('mirror_create','mirror_update','posting_pack','posting_ship','posting_cancel','posting_deliver'));
