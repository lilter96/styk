-- +goose Up
-- The legacy workspace and membership records are retained for referential
-- integrity and audit history. Only this one-to-one binding grants access in
-- merchant mode. Ambiguous legacy ownership is left unbound for an explicit
-- local migration; it is never guessed from names or creation timestamps.
CREATE TABLE saas_merchant_accounts (
 user_id text PRIMARY KEY REFERENCES saas_users(id),
 tenant_id text NOT NULL UNIQUE REFERENCES saas_workspaces(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,user_id) REFERENCES saas_memberships(workspace_id,user_id)
);

INSERT INTO saas_merchant_accounts(user_id,tenant_id)
 SELECT m.user_id,min(m.workspace_id)
 FROM saas_memberships m JOIN saas_workspaces w ON w.id=m.workspace_id
 WHERE m.role='OWNER' AND m.revoked_at IS NULL AND NOT w.legacy_unclaimed
 AND 1=(SELECT count(*) FROM saas_memberships own JOIN saas_workspaces ow ON ow.id=own.workspace_id
        WHERE own.user_id=m.user_id AND own.role='OWNER' AND own.revoked_at IS NULL AND NOT ow.legacy_unclaimed)
 AND 1=(SELECT count(*) FROM saas_memberships o WHERE o.workspace_id=m.workspace_id
        AND o.role='OWNER' AND o.revoked_at IS NULL)
 GROUP BY m.user_id HAVING count(*)=1;

-- Recheck and lock the unique account binding in the same transaction as each
-- protected domain mutation. Historical shared memberships grant no access.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION saas_require_actor(p_tenant text,p_session text,p_admin boolean) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM saas_sessions s
 JOIN saas_users u ON u.id=s.user_id
 JOIN saas_merchant_accounts a ON a.user_id=u.id AND a.tenant_id=p_tenant
 JOIN saas_memberships m ON m.user_id=u.id AND m.workspace_id=a.tenant_id
 JOIN saas_workspaces w ON w.id=a.tenant_id
 WHERE s.audit_id=p_session AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
   AND s.user_auth_version=u.auth_version AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL
   AND m.revoked_at IS NULL AND m.role='OWNER' AND NOT w.legacy_unclaimed
 FOR SHARE OF s,u,a,m;
 IF NOT FOUND THEN RAISE EXCEPTION 'merchant actor is no longer authorized' USING ERRCODE='42501'; END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM saas_merchant_accounts) THEN
  RAISE EXCEPTION 'refusing to discard merchant ownership bindings';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION saas_require_actor(p_tenant text,p_session text,p_admin boolean) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM saas_sessions s
 JOIN saas_users u ON u.id=s.user_id
 JOIN saas_memberships m ON m.user_id=u.id AND m.workspace_id=p_tenant
 JOIN saas_workspaces w ON w.id=m.workspace_id
 WHERE s.audit_id=p_session AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
   AND s.user_auth_version=u.auth_version AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL
   AND m.revoked_at IS NULL AND NOT w.legacy_unclaimed
   AND (m.role IN ('OWNER','ADMIN') OR (NOT p_admin AND m.role='OPERATOR'))
 FOR SHARE OF s,u,m;
 IF NOT FOUND THEN RAISE EXCEPTION 'merchant actor is no longer authorized' USING ERRCODE='42501'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE saas_merchant_accounts;
