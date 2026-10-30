-- Synthetic, operator-attested catalog facts. Never claim provider verification.
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof), facts AS (
SELECT p.*,c.account_id,o.product_id,o.offer_id,o.sku,o.name,o.archived,
substring(o.offer_id from 6)::int n
FROM p JOIN stock_connections c ON c.tenant_id=p.tenant AND c.source='ozon'
JOIN source_imports i ON i.source=c.source AND i.account_id=c.account_id
JOIN ozon_product_observations o ON o.import_id=i.id)
INSERT INTO catalog_sync_snapshots(tenant_id,provider,account_id,entity_kind,entity_id,revision,product,observed_at)
SELECT tenant,'ozon',account_id,'product',product_id,'DEMO-v1-'||md5(offer_id),
jsonb_build_object(
 'identity',jsonb_build_object('account',jsonb_build_object('provider','ozon','id',account_id),'entity_type','product','id',product_id,'offer_id',offer_id,'sku',sku),
 'variant',false,'lifecycle',CASE WHEN archived THEN 'archived' ELSE 'active' END,
 'revision','DEMO-v1-'||md5(offer_id),'observed_at',asof-interval '5 minutes',
 'fields',jsonb_build_object(
  'name',jsonb_build_object('state','known','data',name,'provenance',jsonb_build_object('kind','OPERATOR_ATTESTED','evidence','SYNTHETIC DEVELOPMENT FIXTURE')),
  'price',jsonb_build_object('state','known','data',to_char(((ARRAY[900,1900,700,300,200,1500])[(n-1)%6+1]+((n*137+seed)%5000))::numeric+0.99,'FM9999990.00'),'provenance',jsonb_build_object('kind','OPERATOR_ATTESTED','evidence','SYNTHETIC RUB PRICE; no provider request')),
  'currency',jsonb_build_object('state','known','data','RUB','provenance',jsonb_build_object('kind','OPERATOR_ATTESTED','evidence','SYNTHETIC DEVELOPMENT FIXTURE')))),
asof-interval '5 minutes' FROM facts;
