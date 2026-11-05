package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// Only a validated, immutable, still-unsent leaf admission may be absent from
// its own traversal. Native-free intermediates and every other work remain.
const ownedForestSparseSelfSQL = `SELECT w.work,
 source.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],source.matches
 FROM orders_work w LEFT JOIN orders_previews p ON p.id=w.work->>'preview_id'
 AND p.tenant_id=w.tenant_id AND p.moysklad_account=w.moysklad_account AND p.ozon_account=w.ozon_account
 LEFT JOIN LATERAL(SELECT d->'posting' posting,count(*) OVER() matches
 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.preview->'decisions')='array' THEN p.preview->'decisions' ELSE '[]'::jsonb END) d
 WHERE d#>>'{posting,posting_number}'=w.posting_number
 AND d#>>'{posting,evidence,revision}'=w.work#>>'{command,expected_source_revision}' LIMIT 1) source ON true
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3
 AND w.id=$4 AND w.command_fingerprint=$5 AND w.posting_number=$6
 AND w.action='mirror_create' AND w.dispatch_count=0 AND w.state IN('QUEUED','CLAIMED')
 AND w.work#>>'{command,split_ancestor_settlement,mode}'='node_admission'
 AND w.work#>>'{command,split_ancestor_settlement,source,posting_number}'=$6
 AND w.work#>>'{command,split_ancestor_settlement,node,posting_number}'=$6
 AND w.work#>>'{command,split_ancestor_settlement,split_work_id}'=$8
 AND w.work#>'{command,split_ancestor_settlement,source_ancestors}'->-1->>'posting_number'=$7
 AND EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.work_id=w.id AND f.tenant_id=w.tenant_id
 AND f.moysklad_account=w.moysklad_account AND f.ozon_account=w.ozon_account AND f.posting_number=w.posting_number)`

func readOwnedForestUnsentSelf(ctx context.Context, q ordersQuerier, scope orders.Scope, selfID, fingerprint, selected, node, split string) (bool, error) {
	metadata, err := readScalarMetadataSelf(ctx, q, scope, selfID, fingerprint)
	if err != nil || metadata || selfID == "" || fingerprint == "" || selected == node {
		return metadata, err
	}
	var workRaw, sourceRaw []byte
	var count *int64
	err = q.QueryRow(ctx, ownedForestSparseSelfSQL, append(ordersScopeArgs(scope), selfID, fingerprint, selected, node, split)...).Scan(&workRaw, &sourceRaw, &count)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ordersError("read sealed sparse native admission", err)
	}
	return validOwnedForestSparseSelf(scope, selfID, fingerprint, selected, node, split, workRaw, sourceRaw, count), nil
}

func validOwnedForestSparseSelf(scope orders.Scope, selfID, fingerprint, selected, node, split string, workRaw, sourceRaw []byte, count *int64) bool {
	var work orders.Work
	var source orders.Posting
	if count == nil || *count != 1 || ordersDecode(workRaw, &work) != nil || ordersDecode(sourceRaw, &source) != nil {
		return false
	}
	if work.ID != selfID || work.CommandFingerprint != fingerprint || work.DispatchCount != 0 || work.State != orders.StateQueued && work.State != orders.StateClaimed || !metadataWorkSourceCurrent(work, source, scope) || work.Command.Validate() != nil {
		return false
	}
	proof := work.Command.SplitAncestorSettlement
	if work.Command.Action != orders.ActionMirrorCreate || proof == nil || proof.Mode != orders.SplitAncestorNodeAdmit || proof.SplitWorkID != split || proof.Source.PostingNumber != selected || proof.Node.PostingNumber != selected || len(proof.SourceAncestors) == 0 {
		return false
	}
	return proof.SourceAncestors[len(proof.SourceAncestors)-1].PostingNumber == node && proof.Allows(source) && proof.AllowsCommand(work.Command)
}
