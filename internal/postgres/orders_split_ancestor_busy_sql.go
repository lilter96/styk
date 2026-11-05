package postgres

// Native frontiers wrap their posting; source-only ancestors are plain Posting
// snapshots. Both identity sets participate, including the real native self.
const ownedAncestorDescendantBusySQL = `SELECT EXISTS(SELECT 1
 FROM orders_target_fences f JOIN orders_work pending ON pending.id=f.work_id
 AND pending.tenant_id=f.tenant_id AND pending.moysklad_account=f.moysklad_account
 AND pending.ozon_account=f.ozon_account
 CROSS JOIN LATERAL jsonb_array_elements(
 COALESCE(pending.work#>'{command,split_ancestor_settlement,ancestors}','[]'::jsonb)
 || COALESCE(pending.work#>'{command,split_ancestor_settlement,source_ancestors}','[]'::jsonb)) a
 WHERE f.tenant_id=$1 AND f.moysklad_account=$2 AND f.ozon_account=$3
 AND pending.dispatch_count=1 AND pending.id<>$5
 AND COALESCE(a#>>'{posting,posting_number}',a->>'posting_number')=$4)`

const ownedAncestorBusyGroupsSQL = `SELECT DISTINCT COALESCE(a#>>'{posting,posting_number}',a->>'posting_number')
 FROM orders_target_fences f JOIN orders_work w ON w.id=f.work_id
 AND w.tenant_id=f.tenant_id AND w.moysklad_account=f.moysklad_account
 AND w.ozon_account=f.ozon_account
 CROSS JOIN LATERAL jsonb_array_elements(
 COALESCE(w.work#>'{command,split_ancestor_settlement,ancestors}','[]'::jsonb)
 || COALESCE(w.work#>'{command,split_ancestor_settlement,source_ancestors}','[]'::jsonb)) a
 WHERE f.tenant_id=$1 AND f.moysklad_account=$2 AND f.ozon_account=$3
 AND w.dispatch_count=1 AND COALESCE(a#>>'{posting,posting_number}',a->>'posting_number')=ANY($4::text[]) LIMIT 129`
