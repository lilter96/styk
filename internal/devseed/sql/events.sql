WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO ozon_import_operations(id,tenant_id,provider,account_id,operation_type,idempotency_key,request_fingerprint,principal_type,session_audit_id,limits,counters,phase,outcome,created_at,started_at,finished_at,batch_id,batch_created)
SELECT md5(tenant||c.account_id||'complete'),tenant,c.source,c.account_id,upper(c.source)||'_CATALOG_IMPORT','DEMO-complete-'||c.account_id,md5(c.account_id)||md5(c.account_id),'LOCAL_OPERATOR_SESSION','DEMO-SYNTHETIC-EVIDENCE','{}','{}','COMPLETED','COMPLETE_TRAVERSAL',asof-interval '11 minutes',i.started_at,i.finished_at,i.id,true
FROM p JOIN stock_connections c ON c.tenant_id=p.tenant JOIN source_imports i ON i.source=c.source AND i.account_id=c.account_id;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO outbox_tasks(operation_key,observation_id,state,attempts,max_attempts,result,task_type,import_operation_id,import_account_id,import_tenant_id)
SELECT 'DEMO-import-'||o.id,NULL,'succeeded',1,3,'SYNTHETIC FIXTURE; no provider request',lower(o.operation_type),o.id,o.account_id,o.tenant_id
FROM p JOIN ozon_import_operations o ON o.tenant_id=p.tenant;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO ozon_import_operations(id,tenant_id,provider,account_id,operation_type,idempotency_key,request_fingerprint,principal_type,session_audit_id,limits,counters,phase,outcome,created_at,started_at,finished_at)
SELECT md5(tenant||n::text),tenant,'ozon',CASE WHEN n=4 THEN 'STYK-DEMO-OZON-2' ELSE 'STYK-DEMO-OZON-1' END,'OZON_CATALOG_IMPORT','DEMO-scenario-'||n,md5(n::text)||md5(n::text),'LOCAL_OPERATOR_SESSION','DEMO-SYNTHETIC-EVIDENCE','{}','{}',
CASE n WHEN 1 THEN 'FAILED' WHEN 2 THEN 'CANCELLED' ELSE 'QUEUED' END,CASE n WHEN 1 THEN 'API_FAILURE' WHEN 2 THEN 'CANCELLED' ELSE NULL END,
asof-interval '1 minute',CASE WHEN n=1 THEN asof-interval '1 minute' ELSE NULL END,CASE WHEN n<=2 THEN asof ELSE NULL END
FROM p CROSS JOIN generate_series(1,4) n;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO outbox_tasks(operation_key,observation_id,state,attempts,max_attempts,last_error,available_at,task_type,import_operation_id,import_account_id,import_tenant_id)
SELECT 'DEMO-scenario-'||n,NULL,CASE n WHEN 1 THEN 'failed' WHEN 2 THEN 'cancelled' ELSE 'pending' END,CASE n WHEN 4 THEN 1 WHEN 1 THEN 3 ELSE 0 END,3,
CASE WHEN n IN (1,4) THEN 'SYNTHETIC API failure - no network request' ELSE NULL END,asof+interval '1 day','ozon_catalog_import',md5(tenant||n::text),CASE WHEN n=4 THEN 'STYK-DEMO-OZON-2' ELSE 'STYK-DEMO-OZON-1' END,tenant
FROM p CROSS JOIN generate_series(1,4) n;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO inventory_observations(operation_key,source,sku,quantity)
SELECT 'DEMO-mock-'||n,'demo','DEMO-'||n,(n+seed)%100 FROM p CROSS JOIN generate_series(1,12) n;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO outbox_tasks(operation_key,observation_id,state,attempts,max_attempts,available_at,task_type)
SELECT o.operation_key,o.id,'pending',0,3,asof,'mock' FROM p JOIN inventory_observations o ON o.source='demo';
