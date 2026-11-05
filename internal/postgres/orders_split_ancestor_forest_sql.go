package postgres

// All four native mutation kinds and all states participate. Unreconciled,
// manually changed, or source-less histories must remain blockers, not disappear
// behind a state filter. All graph heads participate, including historical leaf
// native documents that a selected-path-only query would miss. Only the exact
// still-unsent, fenced command being revalidated is excluded; after dispatch it
// must participate again. The caller reads one overflow row to prove the bound.
const ownedForestNativeSQL = `SELECT w.work,r.observed_target,w.created_at,w.dispatched_at,r.created_at,
 EXISTS(SELECT 1 FROM orders_projection_bindings b WHERE b.tenant_id=w.tenant_id
 AND b.moysklad_account=w.moysklad_account AND b.ozon_account=w.ozon_account
 AND b.posting_number=w.posting_number AND b.customer_order_id=r.observed_target#>>'{correlation,customer_order_id}'),
 source.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],source.matches
 FROM orders_work w LEFT JOIN orders_reconciliations r ON r.work_id=w.id
 AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account
 LEFT JOIN orders_previews p ON p.id=w.work->>'preview_id' AND p.tenant_id=w.tenant_id
 AND p.moysklad_account=w.moysklad_account AND p.ozon_account=w.ozon_account
 LEFT JOIN LATERAL(SELECT d->'posting' posting,count(*) OVER() matches
 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.preview->'decisions')='array' THEN p.preview->'decisions' ELSE '[]'::jsonb END) d
 WHERE d#>>'{posting,posting_number}'=w.posting_number
 AND d#>>'{posting,evidence,revision}'=w.work#>>'{command,expected_source_revision}' LIMIT 1) source ON true
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=ANY($4::text[])
 AND w.action IN('mirror_create','mirror_update','mirror_position_update','mirror_composition_update')
 AND NOT COALESCE((` + ownedForestUnsentSelfSQL + `),false)
 ORDER BY w.created_at,w.id LIMIT $11`

// Shared by full native traversal and explicit absence witnesses: a malformed
// or null proof is never absence. Node admission cannot borrow a leaf's mode.
const ownedForestUnsentSelfSQL = `w.id=$5 AND w.command_fingerprint=$6 AND w.dispatch_count=0 AND w.state IN('QUEUED','CLAIMED')
 AND w.posting_number=$7
 AND ((w.work#>>'{command,split_ancestor_settlement,split_work_id}'=$9
 AND ((w.action IN ('mirror_position_update','mirror_composition_update') AND w.posting_number=$8
 AND w.work#>>'{command,split_ancestor_settlement,mode}'='retained_reduction')
 OR (w.action='mirror_create' AND (w.work#>>'{command,split_ancestor_settlement,mode}'='child_admission'
 OR (w.work#>>'{command,split_ancestor_settlement,mode}'='node_admission' AND w.posting_number=$8
 AND w.work#>>'{command,split_ancestor_settlement,source,posting_number}'=$7
 AND w.work#>>'{command,split_ancestor_settlement,node,posting_number}'=$7)))))
 OR (w.action='mirror_update' AND $10::boolean))
 AND EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.work_id=w.id AND f.tenant_id=w.tenant_id
 AND f.moysklad_account=w.moysklad_account AND f.ozon_account=w.ozon_account AND f.posting_number=w.posting_number)`
