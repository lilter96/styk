WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO source_entities(import_id,kind,external_id,name,code,article,parent_id,source_updated)
SELECT i.id,'product','demo-product-'||lpad(n::text,4,'0'),
 (ARRAY['Electronics','Clothing','Home','Cosmetics','Accessories','Sports'])[(n-1)%6+1]||' Demo Item '||lpad(n::text,4,'0'),
 'DEMO-'||lpad(n::text,4,'0'),'DEMO-'||lpad((CASE WHEN n%100=25 THEN n-1 ELSE n END)::text,4,'0'),'',''
FROM p JOIN source_imports i ON i.account_id='STYK-DEMO-MS' CROSS JOIN generate_series(1,500) n;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO source_entities(import_id,kind,external_id,name,code,article,parent_id,source_updated)
SELECT i.id,'store','DEMO-MS-WH-'||n,'Demo Warehouse '||n,'WH-'||n,'','',''
FROM p JOIN source_imports i ON i.account_id='STYK-DEMO-MS' CROSS JOIN generate_series(1,3) n;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO source_warehouse_stock_observations(import_id,kind,external_id,source_href,warehouse_id,warehouse_href,stock,reserve,in_transit,quantity,observed_at)
SELECT i.id,'product','demo-product-'||lpad(n::text,4,'0'),'demo://product/'||n,'DEMO-MS-WH-'||w,'demo://warehouse/'||w,
q,least(q,(n+p.seed)%8),0,NULL,asof-interval '5 minutes'
FROM p JOIN source_imports i ON i.account_id='STYK-DEMO-MS' CROSS JOIN generate_series(1,500) n CROSS JOIN generate_series(1,3) w
CROSS JOIN LATERAL (SELECT CASE WHEN n%17=0 THEN 0 ELSE (n*17+w*23+p.seed)%80 END q) quantities;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO source_stock_observations(import_id,kind,external_id,source_href,stock,reserve,in_transit,quantity,free_stock)
SELECT o.import_id,o.kind,o.external_id,min(o.source_href),sum(stock),sum(reserve),sum(in_transit),sum(stock)-sum(reserve),NULL
FROM p JOIN source_warehouse_stock_observations o ON true GROUP BY o.import_id,o.kind,o.external_id;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO ozon_product_observations(import_id,product_id,offer_id,sku,archived,name)
SELECT i.id,'demo-product-'||lpad(n::text,4,'0'),'DEMO-'||lpad(n::text,4,'0'),'DEMO-SKU-'||n,n%29=0,
 (ARRAY['Electronics','Clothing','Home','Cosmetics','Accessories','Sports'])[(n-1)%6+1]||' Demo Item '||lpad(n::text,4,'0')
FROM p JOIN stock_connections c ON c.tenant_id=p.tenant AND c.source='ozon' JOIN source_imports i ON i.account_id=c.account_id AND i.source=c.source CROSS JOIN generate_series(1,500) n;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO ozon_warehouse_observations(import_id,warehouse_id,name,status,warehouse_type,is_rfbs)
SELECT i.id,'DEMO-OZ-WH-'||w,'Demo FBS Warehouse '||w,'active','FBS',false
FROM p JOIN stock_connections c ON c.tenant_id=p.tenant AND c.source='ozon' JOIN source_imports i ON i.account_id=c.account_id AND i.source=c.source CROSS JOIN generate_series(1,3) w;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO ozon_fbs_stock_observations(import_id,product_id,offer_id,sku,warehouse_id,warehouse_name,present,reserved,free_stock,observed_at)
SELECT i.id,'demo-product-'||lpad(n::text,4,'0'),'DEMO-'||lpad(n::text,4,'0'),'DEMO-SKU-'||n,'DEMO-OZ-WH-'||w,'Demo FBS Warehouse '||w,q,least(q,(n+p.seed)%8),q-least(q,(n+p.seed)%8),asof-interval '5 minutes'
FROM p JOIN stock_connections c ON c.tenant_id=p.tenant AND c.source='ozon' JOIN source_imports i ON i.account_id=c.account_id AND i.source=c.source CROSS JOIN generate_series(1,500) n CROSS JOIN generate_series(1,3) w
CROSS JOIN LATERAL (SELECT CASE WHEN n%17=0 THEN 0 ELSE (n*17+w*23+p.seed)%80 END q) quantities;
