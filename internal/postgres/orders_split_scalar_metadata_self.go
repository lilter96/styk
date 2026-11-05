package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// This witness excludes only the exact unsent metadata command. Its ownership
// baseline is independently rebuilt from an actual inventory reconciliation;
// a JSON baseline or another metadata acknowledgement is never authority.
const splitScalarMetadataSelfSQL = `SELECT self_work.work,inventory_work.work,native_result.observed_target,
 current_source.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],current_source.matches,
 authority_source.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],authority_source.matches
 FROM orders_work self_work
 JOIN orders_work inventory_work ON inventory_work.id=self_work.work#>>'{command,position_baseline,work_id}'
 AND inventory_work.tenant_id=self_work.tenant_id AND inventory_work.moysklad_account=self_work.moysklad_account
 AND inventory_work.ozon_account=self_work.ozon_account AND inventory_work.posting_number=self_work.posting_number
 AND inventory_work.action IN('mirror_create','mirror_position_update','mirror_composition_update')
 AND inventory_work.state='RECONCILED' AND inventory_work.dispatch_count=1 AND inventory_work.created_at<self_work.created_at
 JOIN orders_reconciliations native_result ON native_result.work_id=inventory_work.id AND native_result.tenant_id=inventory_work.tenant_id
 AND native_result.moysklad_account=inventory_work.moysklad_account AND native_result.ozon_account=inventory_work.ozon_account
 AND native_result.observed_target IS NOT NULL AND native_result.created_at<=self_work.created_at
 LEFT JOIN orders_previews current_preview ON current_preview.id=self_work.work->>'preview_id'
 AND current_preview.tenant_id=self_work.tenant_id AND current_preview.moysklad_account=self_work.moysklad_account
 AND current_preview.ozon_account=self_work.ozon_account
 LEFT JOIN orders_previews authority_preview ON authority_preview.id=inventory_work.work->>'preview_id'
 AND authority_preview.tenant_id=inventory_work.tenant_id AND authority_preview.moysklad_account=inventory_work.moysklad_account
 AND authority_preview.ozon_account=inventory_work.ozon_account
 LEFT JOIN LATERAL(SELECT d->'posting' posting,count(*) OVER() matches
 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(current_preview.preview->'decisions')='array' THEN current_preview.preview->'decisions' ELSE '[]'::jsonb END) d
 WHERE d#>>'{posting,posting_number}'=self_work.posting_number
 AND d#>>'{posting,evidence,revision}'=self_work.work#>>'{command,expected_source_revision}' LIMIT 1) current_source ON true
 LEFT JOIN LATERAL(SELECT d->'posting' posting,count(*) OVER() matches
 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(authority_preview.preview->'decisions')='array' THEN authority_preview.preview->'decisions' ELSE '[]'::jsonb END) d
 WHERE d#>>'{posting,posting_number}'=inventory_work.posting_number
 AND d#>>'{posting,evidence,revision}'=inventory_work.work#>>'{command,expected_source_revision}' LIMIT 1) authority_source ON true
 WHERE self_work.tenant_id=$1 AND self_work.moysklad_account=$2 AND self_work.ozon_account=$3
 AND self_work.id=$4 AND self_work.command_fingerprint=$5 AND self_work.action='mirror_update'
 AND self_work.dispatch_count=0 AND self_work.state IN('QUEUED','CLAIMED')
 AND EXISTS(SELECT 1 FROM orders_target_fences fence WHERE fence.work_id=self_work.id
 AND fence.tenant_id=self_work.tenant_id AND fence.moysklad_account=self_work.moysklad_account
 AND fence.ozon_account=self_work.ozon_account AND fence.posting_number=self_work.posting_number)
 AND EXISTS(SELECT 1 FROM orders_projection_bindings binding WHERE binding.tenant_id=inventory_work.tenant_id
 AND binding.moysklad_account=inventory_work.moysklad_account AND binding.ozon_account=inventory_work.ozon_account
 AND binding.posting_number=inventory_work.posting_number
 AND binding.customer_order_id=native_result.observed_target#>>'{correlation,customer_order_id}') LIMIT 2`

type splitScalarMetadataSelf struct {
	current, authority, target, source, original []byte
	sourceCount, originalCount                   *int64
}

func readScalarMetadataSelf(ctx context.Context, q ordersQuerier, scope orders.Scope, selfID, fingerprint string) (bool, error) {
	if selfID == "" || fingerprint == "" {
		return false, nil
	}
	rows, err := q.Query(ctx, splitScalarMetadataSelfSQL, append(ordersScopeArgs(scope), selfID, fingerprint)...)
	if err != nil {
		return false, ordersError("read exact metadata inventory authority", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return false, ordersRowsError("read metadata authority witness", rows.Err())
	}
	var facts splitScalarMetadataSelf
	if err = rows.Scan(&facts.current, &facts.authority, &facts.target, &facts.source, &facts.sourceCount, &facts.original, &facts.originalCount); err != nil {
		return false, ordersError("decode metadata authority witness", err)
	}
	if rows.Next() {
		return false, nil
	}
	if err = rows.Err(); err != nil {
		return false, ordersRowsError("finish metadata authority witness", err)
	}
	return facts.valid(scope, selfID, fingerprint), nil
}

func (f splitScalarMetadataSelf) valid(scope orders.Scope, selfID, fingerprint string) bool {
	if f.sourceCount == nil || *f.sourceCount != 1 || f.originalCount == nil || *f.originalCount != 1 {
		return false
	}
	var current, authority orders.Work
	var target orders.TargetObservation
	var source, original orders.Posting
	for _, pair := range []struct {
		raw []byte
		out any
	}{{f.current, &current}, {f.authority, &authority}, {f.target, &target}, {f.source, &source}, {f.original, &original}} {
		if ordersDecode(pair.raw, pair.out) != nil {
			return false
		}
	}
	if current.ID != selfID || current.CommandFingerprint != fingerprint || current.DispatchCount != 0 || current.State != orders.StateQueued && current.State != orders.StateClaimed || !metadataWorkSourceCurrent(current, source, scope) || !metadataWorkSourceCurrent(authority, original, scope) || target.Scope != scope {
		return false
	}
	return orders.SplitScalarMetadataAuthority(current.Command, authority, target)
}

func metadataWorkSourceCurrent(work orders.Work, source orders.Posting, scope orders.Scope) bool {
	if work.Scope != scope || work.Command.Scope != scope || source.Validate() != nil || source.Scope != scope || source.PostingNumber != work.Command.PostingNumber || source.CommercialOrderID != work.Command.CommercialOrderID || source.Evidence.Revision != work.Command.ExpectedSourceRevision || source.Evidence.Origin != work.Command.SourceOrigin {
		return false
	}
	fingerprint, err := orders.CommandFingerprint(work.Command)
	return err == nil && fingerprint == work.CommandFingerprint
}
