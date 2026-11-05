package postgres

const nativeConsumptionSQL = `SELECT w.work,w.id,w.state,w.dispatch_count,w.command_fingerprint,w.created_at,
 EXISTS(SELECT 1 FROM orders_write_intents d WHERE d.work_id=w.id),
 NOT EXISTS(SELECT 1 FROM orders_write_intents d WHERE d.work_id=w.id AND
 (d.tenant_id<>w.tenant_id OR d.moysklad_account<>w.moysklad_account OR d.ozon_account<>w.ozon_account
 OR d.fingerprint<>w.command_fingerprint OR d.command<>w.work->'command')),
 (i.work_id IS NOT NULL AND w.action=i.kind||'_create' AND i.posting_number=w.posting_number
 AND i.customer_order_id=w.work#>>'{command,correlation,customer_order_id}'
 AND i.event_digest=w.work#>>'{command,native_movement,event,digest}'
 AND i.sync_id::text=w.work#>>'{command,native_movement,correlation,sync_id}'
 AND i.marker=w.work#>>'{command,native_movement,correlation,marker}'
 AND i.basis_document_id::text IS NOT DISTINCT FROM w.work#>>'{command,native_movement,demand_basis,correlation,document_id}'
 AND to_jsonb(i.applicable)=w.work#>'{command,native_movement,applicable,value}'),
 n.native_document_id::text,n.observed_at,r.observed_native_document,r.observed_target,
 COALESCE(t.targets,'[]'::jsonb)
 FROM orders_work w
 LEFT JOIN orders_native_document_intents i ON i.work_id=w.id AND i.tenant_id=w.tenant_id
 AND i.moysklad_account=w.moysklad_account AND i.ozon_account=w.ozon_account
 LEFT JOIN orders_native_document_results n ON n.work_id=w.id AND n.tenant_id=w.tenant_id
 AND n.moysklad_account=w.moysklad_account AND n.ozon_account=w.ozon_account
 LEFT JOIN orders_reconciliations r ON r.work_id=w.id AND r.tenant_id=w.tenant_id
 AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account
 LEFT JOIN LATERAL(SELECT jsonb_agg(jsonb_build_object('position_id',native_order_position_id::text,
 'quantity',quantity,'mapping_id',mapping_id::text,'mapping_version',mapping_version,
 'store_id',store_id,'item_kind',item_kind,'item_id',item_id,'basis_position_id',COALESCE(basis_position_id::text,'')) ORDER BY native_order_position_id) targets
 FROM orders_native_document_targets target WHERE target.work_id=w.id AND target.tenant_id=w.tenant_id
 AND target.moysklad_account=w.moysklad_account AND target.ozon_account=w.ozon_account) t ON true
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3
 AND w.posting_number=$4 AND w.action IN('demand_create','salesreturn_create')
 AND NOT(w.id=$5 AND w.command_fingerprint=$6 AND w.dispatch_count=0 AND w.state IN('QUEUED','CLAIMED')
 AND NOT EXISTS(SELECT 1 FROM orders_write_intents d WHERE d.work_id=w.id)
 AND EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.work_id=w.id AND f.tenant_id=w.tenant_id
 AND f.moysklad_account=w.moysklad_account AND f.ozon_account=w.ozon_account AND f.posting_number=w.posting_number))
 ORDER BY w.created_at,w.id LIMIT $7`
