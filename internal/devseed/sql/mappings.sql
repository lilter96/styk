WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance,confirmed_at)
SELECT tenant,c.account_id,'demo-product-'||lpad(n::text,4,'0'),'DEMO-'||lpad(n::text,4,'0'),'STYK-DEMO-MS','product','demo-product-'||lpad(n::text,4,'0'),1,'SYNTHETIC DEVELOPMENT FIXTURE; not a reviewed provider mapping',asof-interval '4 minutes'
FROM p JOIN stock_connections c ON c.tenant_id=p.tenant AND c.source='ozon' CROSS JOIN generate_series(1,400) n WHERE n%100 NOT IN (24,25);
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO product_mapping_history(mapping_id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,ozon_import_id,moysklad_import_id)
SELECT m.id,m.version,m.tenant_id,m.ozon_account,m.ozon_product_id,m.ozon_offer_id,m.moysklad_account,m.target_kind,m.target_id,m.provenance,m.confirmed_at,oi.id,mi.id
FROM p JOIN product_mappings m ON m.tenant_id=p.tenant JOIN source_imports oi ON oi.account_id=m.ozon_account AND oi.source='ozon' JOIN source_imports mi ON mi.account_id=m.moysklad_account AND mi.source='moysklad';
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO warehouse_mappings(tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,version,provenance,confirmed_at)
SELECT tenant,'STYK-DEMO-MS',c.account_id,'DEMO-MS-WH-'||w,'DEMO-OZ-WH-'||w,'active',1,'SYNTHETIC DEVELOPMENT FIXTURE; not physical identity evidence',asof-interval '4 minutes'
FROM p JOIN stock_connections c ON c.tenant_id=p.tenant AND c.source='ozon' CROSS JOIN generate_series(1,3) w;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO warehouse_mapping_history(mapping_id,version,tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,provenance,confirmed_at,moysklad_import_id,ozon_import_id)
SELECT m.id,m.version,m.tenant_id,m.moysklad_account,m.ozon_account,m.moysklad_warehouse_id,m.ozon_warehouse_id,m.status,m.provenance,m.confirmed_at,mi.id,oi.id
FROM p JOIN warehouse_mappings m ON m.tenant_id=p.tenant JOIN source_imports oi ON oi.account_id=m.ozon_account AND oi.source='ozon' JOIN source_imports mi ON mi.account_id=m.moysklad_account AND mi.source='moysklad';
