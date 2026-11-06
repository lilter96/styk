package console

const adminCompaniesSQL = `WITH companies AS (
 SELECT m.tenant_id, u.id AS user_id, w.name, u.email,
  CASE WHEN u.disabled_at IS NOT NULL THEN 'disabled'
       WHEN u.email_verified_at IS NULL THEN 'unverified' ELSE 'active' END AS status
 FROM saas_merchant_accounts m
 JOIN saas_users u ON u.id=m.user_id
 JOIN saas_workspaces w ON w.id=m.tenant_id
), connected AS (
 SELECT k.tenant_id, k.provider, k.account_id
 FROM connection_credentials k
 JOIN companies c USING (tenant_id)
 LEFT JOIN connection_settings s ON (s.tenant_id,s.source,s.account_id)=(k.tenant_id,k.provider,k.account_id)
 WHERE k.enabled AND NOT COALESCE(s.archived,false)
)`

const adminUsersSQL = adminCompaniesSQL + `, filtered AS (
 SELECT * FROM companies
 WHERE ($2='' OR status=$2)
 AND ($1='' OR strpos(lower(name),lower($1))>0 OR strpos(lower(email),lower($1))>0
      OR strpos(lower(user_id),lower($1))>0 OR strpos(lower(tenant_id),lower($1))>0)
), page AS (
 SELECT * FROM filtered ORDER BY lower(name), tenant_id LIMIT $3 OFFSET $4
), counts AS (
 SELECT k.tenant_id, k.provider, count(*) AS total FROM connected k JOIN page p USING(tenant_id)
 GROUP BY k.tenant_id,k.provider
), totals AS (
 SELECT tenant_id, sum(total) AS connections, jsonb_object_agg(provider,total) AS providers
 FROM counts GROUP BY tenant_id
)
SELECT jsonb_build_object(
 'total',(SELECT count(*) FROM filtered),
 'users',COALESCE(jsonb_agg(jsonb_build_object(
  'id',p.tenant_id,'name',p.name,'email',p.email,'status',p.status,
  'connections',COALESCE(t.connections,0),'providers',COALESCE(t.providers,'{}'::jsonb)
 ) ORDER BY lower(p.name),p.tenant_id),'[]'::jsonb)
) FROM page p LEFT JOIN totals t USING(tenant_id)`

const adminOverviewSQL = adminCompaniesSQL + `, operations AS (
 SELECT o.id,o.tenant_id,o.provider,o.account_id,o.phase,o.finished_at,o.outcome
 FROM ozon_import_operations o JOIN companies c USING(tenant_id)
 WHERE o.phase IN ('COMPLETED','FAILED') AND o.finished_at >= now()-make_interval(days => $1::int)
), latest AS (
 SELECT DISTINCT ON (o.tenant_id,o.provider,o.account_id) o.*
 FROM operations o JOIN connected k USING(tenant_id,provider,account_id)
 ORDER BY o.tenant_id,o.provider,o.account_id,o.finished_at DESC,o.id DESC
), issues AS (
 SELECT l.id,c.name,l.provider,l.finished_at,
  CASE l.outcome WHEN 'API_FAILURE' THEN 'provider_error'
   WHEN 'INVALID_RESPONSE' THEN 'invalid_response'
   WHEN 'LIMIT_REACHED' THEN 'limit_reached'
   WHEN 'CONFIGURATION_UNAVAILABLE' THEN 'configuration'
   WHEN 'PERSISTENCE_FAILURE' THEN 'storage_error'
   ELSE 'import_failed' END AS reason
 FROM latest l JOIN companies c USING(tenant_id)
 WHERE l.phase='FAILED' ORDER BY l.finished_at DESC,l.id DESC LIMIT 8
)
SELECT jsonb_build_object(
 'companies',(SELECT count(*) FROM companies),
 'connections',(SELECT count(*) FROM connected),
 'success',(SELECT count(*) FROM operations WHERE phase='COMPLETED'),
 'errors',(SELECT count(*) FROM operations WHERE phase='FAILED'),
 'providers',COALESCE((SELECT jsonb_object_agg(provider,total)
  FROM (SELECT provider,count(*) AS total FROM connected GROUP BY provider) counts),'{}'::jsonb),
 'issues',COALESCE((SELECT jsonb_agg(to_jsonb(issues) ORDER BY finished_at DESC,id DESC) FROM issues),'[]'::jsonb)
)`
