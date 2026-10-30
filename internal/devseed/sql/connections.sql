WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO stock_connections(tenant_id,source,account_id,provenance,include_import_history,registered_at)
SELECT tenant,'moysklad','STYK-DEMO-MS','SYNTHETIC DEVELOPMENT FIXTURE - NO API CREDENTIALS',true,asof-interval '91 days' FROM p
UNION ALL
SELECT tenant,'ozon','STYK-DEMO-OZON-'||n,'SYNTHETIC DEVELOPMENT FIXTURE - NO API CREDENTIALS',true,asof-interval '91 days' FROM p CROSS JOIN generate_series(1,5) n;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope)
SELECT c.source,c.account_id,'DEMO-v1-'||md5(tenant||seed::text||c.account_id),asof-interval '10 minutes',asof-interval '5 minutes','SYNTHETIC DEVELOPMENT OBSERVATIONS; no provider requests'
FROM p JOIN stock_connections c ON c.tenant_id=p.tenant;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO ozon_import_batches(import_id,traversal_status)
SELECT i.id,'completed' FROM p JOIN stock_connections c ON c.tenant_id=p.tenant AND c.source='ozon'
JOIN source_imports i ON i.source=c.source AND i.account_id=c.account_id;
