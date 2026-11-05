package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// Forest settlement currently proves retained unassembled branches only. A
// superseded or differently assembled graph keeps its existing authority path.
func ownedForestOwnerPath(lineage orders.SplitLineageFacts, selected orders.SplitLineageProof) ([]string, error) {
	owner, err := ownedForestOwner(lineage)
	if err != nil {
		return nil, err
	}
	lineage.SelectedPosting = owner
	proof, err := orders.NewSplitLineage(lineage)
	if err != nil {
		return nil, err
	}
	path, original := proof.PathWorkIDs(), selected.PathWorkIDs()
	if len(path) == 0 || len(original) == 0 || path[len(path)-1] == original[0] {
		return nil, orders.ErrBlocked // Existing wire cannot express root==split.
	}
	return path, nil
}

func ownedForestOwner(lineage orders.SplitLineageFacts) (string, error) {
	owner, births := "", 0
	input := false
	for _, step := range lineage.Splits {
		if step.Work.Command.Action != orders.ActionPostingSplitUnassembled || !deferredRetainsRoot(step) {
			return "", orders.ErrBlocked
		}
		input = input || step.Before.PostingNumber == lineage.SelectedPosting
		for _, output := range step.Work.Receipt.ObservedPostings {
			if output.PostingNumber == lineage.SelectedPosting && output.PostingNumber != step.Before.PostingNumber {
				owner, births = step.Before.PostingNumber, births+1
			}
		}
	}
	if input {
		return lineage.SelectedPosting, nil
	}
	if births != 1 {
		return "", orders.ErrBlocked
	}
	return owner, nil
}

func ownedForestHasNativeAnchor(ctx context.Context, q ordersQuerier, lineage orders.SplitLineageFacts, selected, selfID, fingerprint, split string) (bool, error) {
	inputs := make([]string, 0, len(lineage.Splits))
	seen := make(map[string]bool, len(lineage.Splits))
	for _, step := range lineage.Splits {
		number := step.Before.PostingNumber
		if !seen[number] {
			seen[number] = true
			inputs = append(inputs, number)
		}
	}
	var exists bool
	err := q.QueryRow(ctx, ownedForestAnchorSQL, append(ordersScopeArgs(lineage.Scope), inputs, selfID, fingerprint, selected, split)...).Scan(&exists)
	return exists, ordersRowsError("read native forest anchor", err)
}

// Leaf-native histories alone are not evidence that any split input reserved a
// native pool. Do not let the exact unsent legacy native-first create fabricate
// an anchor for its own dispatch. UNKNOWN/dispatched/other attempts remain.
const ownedForestAnchorSQL = `SELECT EXISTS(SELECT 1 FROM orders_work w
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3
 AND w.posting_number=ANY($4::text[])
 AND w.action IN('mirror_create','mirror_update','mirror_position_update','mirror_composition_update')
 AND NOT COALESCE((w.id=$5 AND w.command_fingerprint=$6 AND w.dispatch_count=0
 AND w.state IN('QUEUED','CLAIMED') AND w.action='mirror_create' AND w.posting_number=$7
 AND w.work#>>'{command,split_source_work_id}'=$8
 AND EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.work_id=w.id AND f.tenant_id=w.tenant_id
 AND f.moysklad_account=w.moysklad_account AND f.ozon_account=w.ozon_account AND f.posting_number=w.posting_number)),false))`
