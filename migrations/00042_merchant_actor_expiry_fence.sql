-- +goose Up
-- A clock predicate can be evaluated before a row-lock wait. Lock the actor
-- lineage first, then recheck authorization and expiry while those locks hold.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION saas_require_actor(p_tenant text,p_session text,p_admin boolean) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM saas_sessions s
 JOIN saas_users u ON u.id=s.user_id
 JOIN saas_merchant_accounts a ON a.user_id=u.id AND a.tenant_id=p_tenant
 JOIN saas_memberships m ON m.user_id=u.id AND m.workspace_id=a.tenant_id
 JOIN saas_workspaces w ON w.id=a.tenant_id
 WHERE s.audit_id=p_session
 FOR SHARE OF s,u,a,m,w;
 IF NOT FOUND THEN RAISE EXCEPTION 'merchant actor is no longer authorized' USING ERRCODE='42501'; END IF;

 PERFORM 1 FROM saas_sessions s
 JOIN saas_users u ON u.id=s.user_id
 JOIN saas_merchant_accounts a ON a.user_id=u.id AND a.tenant_id=p_tenant
 JOIN saas_memberships m ON m.user_id=u.id AND m.workspace_id=a.tenant_id
 JOIN saas_workspaces w ON w.id=a.tenant_id
 WHERE s.audit_id=p_session AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
   AND s.user_auth_version=u.auth_version AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL
   AND m.revoked_at IS NULL AND m.role='OWNER' AND NOT w.legacy_unclaimed;
 IF NOT FOUND THEN RAISE EXCEPTION 'merchant actor is no longer authorized' USING ERRCODE='42501'; END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Keep the security correction on rollback: it is compatible with migration
-- 39 and owns no data. Migration 39 restores its predecessor if rolled back.
SELECT 1;
