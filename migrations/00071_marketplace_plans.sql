-- +goose Up
ALTER TABLE saas_workspaces
 ADD COLUMN marketplace_plan text NOT NULL DEFAULT 'plan_1'
  CHECK (marketplace_plan IN ('plan_1','plan_2','plan_3','plan_4')),
 ADD COLUMN marketplace_plan_revision bigint NOT NULL DEFAULT 1
  CHECK (marketplace_plan_revision > 0);

-- A registration without a stored credential still permits the legacy
-- environment fallback. An explicit disabled revision blocks it.
CREATE VIEW marketplace_account_usage AS
 SELECT c.tenant_id,c.source AS provider,count(*)::integer AS used
 FROM stock_connections c
 LEFT JOIN connection_credentials k ON (k.tenant_id,k.provider,k.account_id)=(c.tenant_id,c.source,c.account_id)
 WHERE c.source<>'moysklad' AND COALESCE(k.enabled,true)
 GROUP BY c.tenant_id,c.source;

WITH usage AS (
 SELECT tenant_id,sum(used) AS total,max(used) AS per_provider
 FROM marketplace_account_usage GROUP BY tenant_id
)
UPDATE saas_workspaces w SET marketplace_plan=CASE
 WHEN u.total<=1 THEN 'plan_1'
 WHEN u.total<=2 AND u.per_provider<=1 THEN 'plan_2'
 WHEN u.total<=5 THEN 'plan_3'
 ELSE 'plan_4' END
FROM usage u WHERE w.id=u.tenant_id;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM saas_workspaces WHERE marketplace_plan_revision>1 OR marketplace_plan<>'plan_1')
 OR EXISTS(SELECT 1 FROM saas_security_events WHERE action='MARKETPLACE_PLAN_CHANGED') THEN
  RAISE EXCEPTION 'retain assigned marketplace plans and audit history; roll back the application with schema 71 in place';
 END IF;
END $$;
-- +goose StatementEnd
DROP VIEW marketplace_account_usage;
ALTER TABLE saas_workspaces DROP COLUMN marketplace_plan_revision,DROP COLUMN marketplace_plan;
