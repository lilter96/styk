-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION orders_finance_cipher_valid(chunks bytea[],body_bytes integer) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE chunk bytea; actual_bytes integer:=0;
BEGIN
 IF chunks IS NULL OR body_bytes<1 OR body_bytes>4194304 OR cardinality(chunks)<>(body_bytes+65535)/65536 THEN RETURN false; END IF;
 FOREACH chunk IN ARRAY chunks LOOP
  IF chunk IS NULL OR octet_length(chunk)<=28 OR octet_length(chunk)>65564 THEN RETURN false; END IF;
  actual_bytes:=actual_bytes+octet_length(chunk)-28;
 END LOOP;
 RETURN actual_bytes=body_bytes;
END $$;
-- +goose StatementEnd
CREATE TABLE orders_finance_raw_evidence (
 tenant_id text NOT NULL,moysklad_account text NOT NULL,ozon_account text NOT NULL,
 report_year integer NOT NULL,report_month integer NOT NULL CHECK(report_month BETWEEN 1 AND 12),
 credential_revision bigint NOT NULL CHECK(credential_revision>0),
 source_fingerprint text NOT NULL CHECK(source_fingerprint ~ '^[0-9a-f]{64}$'),
 raw_digest text NOT NULL CHECK(raw_digest ~ '^[0-9a-f]{64}$'),
 body_bytes integer NOT NULL CHECK(body_bytes BETWEEN 1 AND 4194304),
 ciphertext bytea[] NOT NULL CHECK(orders_finance_cipher_valid(ciphertext,body_bytes)),
 observed_at timestamptz NOT NULL,recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,report_year,report_month,credential_revision,source_fingerprint,raw_digest),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,report_year,report_month,source_fingerprint)
 REFERENCES orders_finance_observations(tenant_id,moysklad_account,ozon_account,report_year,report_month,fingerprint)
);
CREATE TRIGGER finance_raw_evidence_immutable BEFORE UPDATE OR DELETE ON orders_finance_raw_evidence FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_finance_raw_evidence) THEN RAISE EXCEPTION 'encrypted financial source history prevents rollback; use a forward fix'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_finance_raw_evidence;
DROP FUNCTION orders_finance_cipher_valid(bytea[],integer);
