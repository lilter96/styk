-- +goose Up
-- Provider secrets are application-encrypted with a dedicated operator-supplied
-- key. The database contains no plaintext credential or secret-bearing payload.
CREATE TABLE connection_credentials (
 tenant_id text NOT NULL,
 provider text NOT NULL CHECK(provider IN ('moysklad','ozon')),
 account_id text NOT NULL,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 120 AND name=trim(name)),
 revision bigint NOT NULL CHECK(revision>0),
 enabled boolean NOT NULL DEFAULT true,
 ciphertext bytea,
 CHECK((enabled AND ciphertext IS NOT NULL AND octet_length(ciphertext) BETWEEN 29 AND 8240) OR (NOT enabled AND ciphertext IS NULL)),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 checked_at timestamptz NOT NULL,
 last_request_id text NOT NULL CHECK(length(last_request_id) BETWEEN 1 AND 128),
 PRIMARY KEY(tenant_id,provider,account_id),
 FOREIGN KEY(tenant_id,provider,account_id) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE TABLE credential_save_requests (
 tenant_id text NOT NULL,
 provider text NOT NULL CHECK(provider IN ('moysklad','ozon')),
 request_id text NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
 request_digest bytea NOT NULL CHECK(octet_length(request_digest)=32),
 account_id text NOT NULL,
 name text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 enabled boolean NOT NULL,
 checked_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,provider,request_id),
 FOREIGN KEY(tenant_id,provider,account_id) REFERENCES connection_credentials(tenant_id,provider,account_id)
);
CREATE INDEX credential_recent_provider ON connection_credentials(tenant_id,provider,updated_at DESC);


-- +goose StatementBegin
CREATE FUNCTION credential_history_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'credential request history is immutable';
END $$;
-- +goose StatementEnd
CREATE TRIGGER credential_history_guard BEFORE UPDATE OR DELETE ON credential_save_requests FOR EACH ROW EXECUTE FUNCTION credential_history_guard();
-- +goose StatementBegin
CREATE FUNCTION credential_revision_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'disconnect credentials through a new revision'; END IF;
 IF (NEW.tenant_id,NEW.provider,NEW.account_id) IS DISTINCT FROM (OLD.tenant_id,OLD.provider,OLD.account_id) OR NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'credential scope is immutable and revision must advance once'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER credential_revision_guard BEFORE UPDATE OR DELETE ON connection_credentials FOR EACH ROW EXECUTE FUNCTION credential_revision_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM connection_credentials) OR EXISTS(SELECT 1 FROM credential_save_requests) THEN
  RAISE EXCEPTION 'refusing to discard credentials or credential request history';
 END IF;
END; $$;
-- +goose StatementEnd
DROP TABLE credential_save_requests;
DROP FUNCTION credential_history_guard();
DROP TABLE connection_credentials;
DROP FUNCTION credential_revision_guard();
