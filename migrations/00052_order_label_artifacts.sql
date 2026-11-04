-- +goose Up
CREATE TABLE orders_label_artifacts (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 task_id text NOT NULL,
 label_type text NOT NULL CHECK(label_type IN ('big_label','small_label')),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 plain_size integer NOT NULL CHECK(plain_size BETWEEN 1 AND 8388608),
 ciphertext bytea NOT NULL CHECK(octet_length(ciphertext) BETWEEN 29 AND 12582912),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>created_at AND expires_at<=created_at+interval '1 day'),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account)
);
CREATE INDEX orders_label_artifacts_expiry ON orders_label_artifacts(expires_at);
CREATE TRIGGER orders_label_artifacts_no_update BEFORE UPDATE ON orders_label_artifacts
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_label_artifacts) THEN
  RAISE EXCEPTION 'encrypted label artifacts prevent destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_label_artifacts;
