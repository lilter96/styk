-- +goose Up
CREATE TABLE saas_users (
 id text PRIMARY KEY CHECK(id ~ '^u_[0-9a-f]{32}$'),
 email text NOT NULL UNIQUE CHECK(email=lower(email) AND length(email) BETWEEN 3 AND 254),
 password_hash bytea NOT NULL CHECK(octet_length(password_hash) BETWEEN 40 AND 128),
 auth_version bigint NOT NULL DEFAULT 1 CHECK(auth_version>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 email_verified_at timestamptz,
 disabled_at timestamptz
);

CREATE TABLE saas_workspaces (
 id text PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 256 AND id=trim(id)),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 120 AND name=trim(name)),
 legacy_unclaimed boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Reserve existing identifiers but give no user access to historical records.
INSERT INTO saas_workspaces(id,name,legacy_unclaimed)
 SELECT tenant_id,rtrim(left(tenant_id,120)),true FROM (
  SELECT tenant_id FROM stock_connections
  UNION SELECT tenant_id FROM product_mappings
  UNION SELECT tenant_id FROM warehouse_mappings
  UNION SELECT tenant_id FROM stock_policies
  UNION SELECT tenant_id FROM stock_publications
  UNION SELECT tenant_id FROM moy_order_batches
  UNION SELECT tenant_id FROM ozon_posting_batches
 ) legacy;

CREATE TABLE saas_memberships (
 workspace_id text NOT NULL REFERENCES saas_workspaces(id),
 user_id text NOT NULL REFERENCES saas_users(id),
 role text NOT NULL CHECK(role IN ('OWNER','ADMIN','OPERATOR','VIEWER')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 revoked_at timestamptz,
 PRIMARY KEY(workspace_id,user_id)
);
CREATE INDEX saas_memberships_user ON saas_memberships(user_id,workspace_id) WHERE revoked_at IS NULL;

CREATE TABLE saas_sessions (
 audit_id text PRIMARY KEY CHECK(audit_id ~ '^user\.[A-Za-z0-9_-]{22}$'),
 token_hash bytea NOT NULL UNIQUE CHECK(octet_length(token_hash)=32),
 csrf_token text NOT NULL CHECK(length(csrf_token)=43),
 user_id text NOT NULL REFERENCES saas_users(id),
 user_auth_version bigint NOT NULL CHECK(user_auth_version>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '8 hours')
);
CREATE INDEX saas_sessions_user ON saas_sessions(user_id) WHERE revoked_at IS NULL;

CREATE TABLE saas_password_resets (
 token_hash bytea PRIMARY KEY CHECK(octet_length(token_hash)=32),
 user_id text NOT NULL REFERENCES saas_users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 used_at timestamptz,
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '30 minutes')
);
CREATE INDEX saas_password_resets_user ON saas_password_resets(user_id);

CREATE TABLE saas_email_verifications (
 token_hash bytea PRIMARY KEY CHECK(octet_length(token_hash)=32),
 user_id text NOT NULL REFERENCES saas_users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 used_at timestamptz,
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '24 hours')
);
CREATE INDEX saas_email_verifications_user ON saas_email_verifications(user_id);

CREATE TABLE saas_security_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 workspace_id text REFERENCES saas_workspaces(id),
 actor_user_id text REFERENCES saas_users(id),
 session_audit_id text,
 action text NOT NULL CHECK(length(action) BETWEEN 1 AND 80),
 target text NOT NULL DEFAULT '' CHECK(length(target)<=256),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX saas_security_events_scope ON saas_security_events(workspace_id,id DESC);
CREATE TRIGGER saas_security_events_immutable BEFORE UPDATE OR DELETE ON saas_security_events FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

CREATE TABLE saas_auth_limits (
 bucket bytea PRIMARY KEY CHECK(octet_length(bucket)=32),
 window_started_at timestamptz NOT NULL,
 attempts integer NOT NULL CHECK(attempts>0)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM saas_users) OR EXISTS(SELECT 1 FROM saas_memberships)
    OR EXISTS(SELECT 1 FROM saas_sessions) OR EXISTS(SELECT 1 FROM saas_security_events)
    OR EXISTS(SELECT 1 FROM saas_password_resets) OR EXISTS(SELECT 1 FROM saas_email_verifications) THEN
  RAISE EXCEPTION 'refusing to discard SaaS identities or audit history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE saas_auth_limits;
DROP TABLE saas_security_events;
DROP TABLE saas_email_verifications;
DROP TABLE saas_password_resets;
DROP TABLE saas_sessions;
DROP TABLE saas_memberships;
DROP TABLE saas_workspaces;
DROP TABLE saas_users;
