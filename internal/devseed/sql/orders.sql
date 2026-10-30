WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO ozon_posting_batches(tenant_id,account_id,window_from,window_to,started_at,finished_at,traversal_status)
SELECT tenant,c.account_id,asof-interval '90 days',asof,asof-interval '3 minutes',asof-interval '2 minutes','completed'
FROM p JOIN stock_connections c ON c.tenant_id=p.tenant AND c.source='ozon';
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO ozon_posting_identities(tenant_id,account_id,posting_number,order_id)
SELECT tenant,'STYK-DEMO-OZON-'||((n-1)%5+1),'DEMO-POST-'||n,'DEMO-ORDER-'||n FROM p CROSS JOIN generate_series(1,1000) n;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO ozon_posting_observations(batch_id,tenant_id,account_id,posting_number,order_id,order_number,status,substatus,warehouse_id,in_process_at,shipment_date,delivering_date,cancellation_reason,cancellation_type,cancellation_initiator,observed_at,observed_time_basis,source_created_at,source_updated_at)
SELECT b.id,tenant,b.account_id,'DEMO-POST-'||n,'DEMO-ORDER-'||n,'DEMO-ORDER-'||n,
(ARRAY['awaiting_packaging','awaiting_deliver','delivering','delivered','cancelled'])[(n/5)%5+1],'','DEMO-OZ-WH-'||((n-1)%3+1),'','','',NULL,NULL,NULL,asof-interval '2 minutes','detail_received',
asof-interval '1 minute'-((n*7)%90)*interval '1 day',asof-interval '1 minute'-((n*7)%90)*interval '1 day'
FROM p JOIN ozon_posting_batches b ON b.tenant_id=p.tenant CROSS JOIN generate_series(1,1000) n WHERE b.account_id='STYK-DEMO-OZON-'||((n-1)%5+1);
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO ozon_posting_lines(observation_id,line_no,offer_id,sku,quantity)
SELECT o.id,1,'DEMO-'||lpad(((substring(o.posting_number from 11)::int-1)%500+1)::text,4,'0'),'DEMO-SKU-'||((substring(o.posting_number from 11)::int-1)%500+1),1+substring(o.posting_number from 11)::int%3
FROM p JOIN ozon_posting_observations o ON o.tenant_id=p.tenant;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO moy_order_batches(tenant_id,account_id,window_from,window_to,started_at,finished_at,traversal_status)
SELECT tenant,'STYK-DEMO-MS',asof-(w+1)*interval '30 days',asof-w*interval '30 days',asof-interval '3 minutes',asof-interval '2 minutes','completed' FROM p CROSS JOIN generate_series(0,2) w;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO moy_order_identities(tenant_id,account_id,order_id)
SELECT tenant,'STYK-DEMO-MS','demo-ms-order-'||n FROM p CROSS JOIN generate_series(1,1000) n;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO moy_order_observations(batch_id,tenant_id,account_id,order_id,sync_id,state_id,warehouse_id,source_created,source_updated,source_moment,applicable,observed_at)
SELECT b.id,tenant,'STYK-DEMO-MS','demo-ms-order-'||n,'DEMO-POST-'||n,'demo-state-'||((n/5)%5+1),'DEMO-MS-WH-'||((n-1)%3+1),
to_char(asof-interval '1 minute'-((n*7)%90)*interval '1 day','YYYY-MM-DD HH24:MI:SS'),to_char(asof-interval '1 minute'-((n*7)%90)*interval '1 day','YYYY-MM-DD HH24:MI:SS'),to_char(asof-interval '1 minute'-((n*7)%90)*interval '1 day','YYYY-MM-DD HH24:MI:SS'),(n/5)%5<>4,asof-interval '2 minutes'
FROM p JOIN moy_order_batches b ON b.tenant_id=p.tenant CROSS JOIN generate_series(1,1000) n WHERE asof-interval '1 minute'-((n*7)%90)*interval '1 day'>=b.window_from AND asof-interval '1 minute'-((n*7)%90)*interval '1 day'<b.window_to;
-- statement
WITH p AS (SELECT $1::text tenant,$2::bigint seed,$3::timestamptz asof)
INSERT INTO moy_order_positions(observation_id,tenant_id,account_id,position_id,assortment_kind,assortment_id,quantity,reserve,shipped,observed_at)
SELECT o.id,tenant,'STYK-DEMO-MS','demo-position-'||n,'product','demo-product-'||lpad(((n-1)%500+1)::text,4,'0'),1+n%3,CASE WHEN (n/5)%5 IN (0,1) AND stock.reserve>0 THEN 1 ELSE 0 END,CASE WHEN (n/5)%5 IN (2,3) THEN 1+n%3 ELSE 0 END,asof-interval '2 minutes'
FROM p JOIN moy_order_observations o ON o.tenant_id=p.tenant CROSS JOIN LATERAL (SELECT substring(o.order_id from 15)::int n) x JOIN source_warehouse_stock_observations stock ON stock.external_id='demo-product-'||lpad(((n-1)%500+1)::text,4,'0') AND stock.warehouse_id=o.warehouse_id;
