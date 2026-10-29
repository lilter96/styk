-- +goose Up
-- Provider budgets belong to the external account, irrespective of tenant,
-- process, worker or HTTP client. No connection is retained during HTTP or sleep.
CREATE TABLE provider_call_accounts (
 provider text NOT NULL CHECK(provider IN ('moysklad','ozon')),
 account_id text NOT NULL CHECK(length(account_id) BETWEEN 1 AND 256),
 interval_ns bigint NOT NULL CHECK(interval_ns BETWEEN 1 AND 1000000000),
 max_inflight integer NOT NULL CHECK(max_inflight BETWEEN 1 AND 20),
 next_send_at timestamptz NOT NULL DEFAULT '-infinity',
 cooldown_until timestamptz NOT NULL DEFAULT '-infinity',
 cooldown_blocked boolean NOT NULL DEFAULT false,
 attempts bigint NOT NULL DEFAULT 0 CHECK(attempts>=0),
 completed bigint NOT NULL DEFAULT 0 CHECK(completed>=0),
 rate_limited bigint NOT NULL DEFAULT 0 CHECK(rate_limited>=0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(provider,account_id)
);
CREATE TABLE provider_call_methods (
 provider text NOT NULL,
 account_id text NOT NULL,
 method_key text NOT NULL CHECK(method_key ~ '^[0-9a-f]{64}$'),
 cooldown_until timestamptz NOT NULL DEFAULT '-infinity',
 attempts bigint NOT NULL DEFAULT 0 CHECK(attempts>=0),
 PRIMARY KEY(provider,account_id,method_key),
 FOREIGN KEY(provider,account_id) REFERENCES provider_call_accounts ON DELETE CASCADE
);
CREATE TABLE provider_call_targets (
 provider text NOT NULL,
 account_id text NOT NULL,
 target_key text NOT NULL CHECK(target_key ~ '^[0-9a-f]{64}$'),
 next_send_at timestamptz NOT NULL DEFAULT '-infinity',
 PRIMARY KEY(provider,account_id,target_key),
 FOREIGN KEY(provider,account_id) REFERENCES provider_call_accounts ON DELETE CASCADE
);
CREATE TABLE provider_call_rejected_credentials (
 provider text NOT NULL,
 account_id text NOT NULL,
 tenant_id text NOT NULL CHECK(length(tenant_id) BETWEEN 1 AND 256),
 credential_revision bigint NOT NULL CHECK(credential_revision>=0),
 rejected_at timestamptz NOT NULL,
 PRIMARY KEY(provider,account_id,tenant_id,credential_revision),
 FOREIGN KEY(provider,account_id) REFERENCES provider_call_accounts ON DELETE CASCADE
);
CREATE TABLE provider_call_permits (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'),
 provider text NOT NULL,
 account_id text NOT NULL,
 tenant_id text NOT NULL CHECK(length(tenant_id) BETWEEN 1 AND 256),
 credential_revision bigint NOT NULL CHECK(credential_revision>=0),
 method_key text NOT NULL CHECK(method_key ~ '^[0-9a-f]{64}$'),
 operation_key text NOT NULL CHECK(operation_key ~ '^[0-9a-f]{64}$'),
 target_key text NOT NULL CHECK(target_key='' OR target_key ~ '^[0-9a-f]{64}$'),
 is_write boolean NOT NULL,
 state text NOT NULL CHECK(state IN ('reserved','sending','finished','aborted','expired')),
 created_at timestamptz NOT NULL,
 lease_until timestamptz NOT NULL,
 started_at timestamptz,
 finished_at timestamptz,
 response_status integer NOT NULL DEFAULT 0 CHECK(response_status BETWEEN 0 AND 599),
 FOREIGN KEY(provider,account_id) REFERENCES provider_call_accounts ON DELETE CASCADE,
 FOREIGN KEY(provider,account_id,method_key) REFERENCES provider_call_methods,
 CHECK(lease_until>created_at),
 CHECK((state='reserved' AND started_at IS NULL) OR state<>'reserved'),
 CHECK(state<>'sending' OR started_at IS NOT NULL)
);
CREATE INDEX provider_call_permits_active ON provider_call_permits(provider,account_id,lease_until) WHERE state IN ('reserved','sending');
CREATE INDEX provider_call_permits_history ON provider_call_permits(provider,account_id,finished_at) WHERE state IN ('finished','aborted','expired');

-- +goose Down
-- Reverting discards coordination history. Stop all senders before rollback.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM provider_call_permits WHERE state IN ('reserved','sending') AND lease_until>clock_timestamp())
 OR EXISTS(SELECT 1 FROM provider_call_accounts WHERE attempts>0 OR cooldown_blocked OR GREATEST(next_send_at,cooldown_until)>clock_timestamp())
 OR EXISTS(SELECT 1 FROM provider_call_rejected_credentials)
 OR EXISTS(SELECT 1 FROM provider_call_methods WHERE cooldown_until>clock_timestamp())
 OR EXISTS(SELECT 1 FROM provider_call_targets WHERE next_send_at>clock_timestamp()) THEN
  RAISE EXCEPTION 'refusing to discard active provider coordination or attempt history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE provider_call_permits;
DROP TABLE provider_call_rejected_credentials;
DROP TABLE provider_call_targets;
DROP TABLE provider_call_methods;
DROP TABLE provider_call_accounts;
