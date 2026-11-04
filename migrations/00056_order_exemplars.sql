-- +goose Up
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('mirror_create','mirror_update','mirror_position_update','posting_pack','posting_ship','posting_cancel','posting_deliver',
 'posting_split','posting_partial_cancel','posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create',
 'posting_exemplar_prepare','posting_exemplar_set'));
CREATE TABLE orders_exemplar_secrets (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 target_fingerprint text NOT NULL CHECK(target_fingerprint ~ '^[0-9a-f]{64}$'),
 credential_revision bigint NOT NULL CHECK(credential_revision>0),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 public_snapshot jsonb NOT NULL CHECK(jsonb_typeof(public_snapshot)='object'),
 ciphertext bytea NOT NULL CHECK(octet_length(ciphertext) BETWEEN 29 AND 132096),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>created_at AND expires_at<=created_at+interval '1 day'),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account)
);
CREATE INDEX orders_exemplar_expiry ON orders_exemplar_secrets(expires_at);
CREATE INDEX orders_exemplar_posting ON orders_exemplar_secrets(tenant_id,moysklad_account,ozon_account,posting_number,created_at DESC);
CREATE TRIGGER orders_exemplar_no_update BEFORE UPDATE ON orders_exemplar_secrets FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_work WHERE action IN ('posting_exemplar_prepare','posting_exemplar_set')) THEN
  RAISE EXCEPTION 'exemplar intent history prevents destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_exemplar_secrets;
ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check;
ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN
 ('mirror_create','mirror_update','mirror_position_update','posting_pack','posting_ship','posting_cancel','posting_deliver',
 'posting_split','posting_partial_cancel','posting_tracking','posting_last_mile','posting_sent_by_seller','posting_label_create'));
