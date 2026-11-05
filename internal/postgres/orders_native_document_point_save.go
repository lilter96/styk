package postgres

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func saveNativeDocumentPointRead(ctx context.Context, tx pgx.Tx, page orders.SourcePage, read orders.NativeDocumentPointRead, pageID string, admitted, now time.Time) error {
	if read.Validate() != nil || read.Parent.Scope != page.Scope || read.CredentialRevision != page.Credentials.Moysklad || read.StartedAt.Before(page.StartedAt) || read.FinishedAt.After(page.FinishedAt) || read.FinishedAt.After(now.Add(5*time.Second)) {
		return orders.ErrStale
	}
	bound, err := readNativePointBinding(ctx, tx, page.Scope, read.Parent.PostingNumber)
	if err != nil {
		return err
	}
	if bound != read.Parent {
		return orders.ErrStale
	}
	if err = validateNativeDocumentPointIdentity(ctx, tx, read); err != nil {
		return err
	}
	var snapshot, observed any
	if read.Observation != nil {
		id, e := saveNativeDocumentPointSnapshot(ctx, tx, read, now)
		if e != nil {
			return e
		}
		snapshot, observed = id, read.Observation.ObservedAt
	}
	args := append(ordersScopeArgs(page.Scope), read.WorkID, read.Correlation.Kind, read.Correlation.DocumentID, read.Parent.PostingNumber, read.Parent.CustomerOrderID, read.CredentialRevision, pageID, snapshot, read.Code, read.StartedAt, read.FinishedAt, observed, admitted, now)
	_, err = tx.Exec(ctx, `INSERT INTO orders_native_document_reads(tenant_id,moysklad_account,ozon_account,work_id,kind,native_document_id,posting_number,customer_order_id,credential_revision,page_id,snapshot_id,code,started_at,finished_at,observed_at,admitted_at,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, args...)
	return ordersRowsError("save native document point attempt", err)
}

func validateNativeDocumentPointIdentity(ctx context.Context, tx pgx.Tx, read orders.NativeDocumentPointRead) error {
	var valid bool
	args := append(ordersScopeArgs(read.Parent.Scope), read.WorkID, read.Correlation.Kind, read.Correlation.DocumentID, read.Parent.PostingNumber, read.Parent.CustomerOrderID, read.Correlation.SyncID, read.Correlation.Marker)
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_native_document_results n
 JOIN orders_native_document_intents i ON i.work_id=n.work_id AND i.tenant_id=n.tenant_id
 AND i.moysklad_account=n.moysklad_account AND i.ozon_account=n.ozon_account
 WHERE n.tenant_id=$1 AND n.moysklad_account=$2 AND n.ozon_account=$3 AND n.work_id=$4
 AND n.kind=$5 AND n.native_document_id=$6 AND n.posting_number=$7 AND n.customer_order_id=$8
 AND i.sync_id=$9 AND i.marker=$10)`, args...).Scan(&valid)
	if err != nil {
		return ordersError("verify exact native document read identity", err)
	}
	if !valid {
		return orders.ErrStale
	}
	return nil
}

func saveNativeDocumentPointSnapshot(ctx context.Context, tx pgx.Tx, read orders.NativeDocumentPointRead, now time.Time) (int64, error) {
	hash, err := nativeDocumentPointHash(*read.Observation)
	if err != nil {
		return 0, err
	}
	raw, err := ordersJSON(read.Observation)
	if err != nil {
		return 0, err
	}
	args := append(ordersScopeArgs(read.Parent.Scope), read.WorkID, read.Correlation.Kind, read.Correlation.DocumentID, read.Parent.PostingNumber, read.Parent.CustomerOrderID, hash, raw, now)
	_, err = tx.Exec(ctx, `INSERT INTO orders_native_document_snapshots(tenant_id,moysklad_account,ozon_account,work_id,kind,native_document_id,posting_number,customer_order_id,facts_hash,observation,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(work_id,facts_hash) DO NOTHING`, args...)
	if err != nil {
		return 0, ordersError("save immutable native document snapshot", err)
	}
	var id int64
	err = tx.QueryRow(ctx, `SELECT id FROM orders_native_document_snapshots WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND work_id=$4 AND facts_hash=$5`, append(ordersScopeArgs(read.Parent.Scope), read.WorkID, hash)...).Scan(&id)
	return id, ordersRowsError("read native document snapshot identity", err)
}

func nativeDocumentPointHash(observation orders.NativeDocumentObservation) (string, error) {
	observation.ObservedAt = time.Time{}
	observation.ReturnsStartedAt, observation.ReturnsFinishedAt = time.Time{}, time.Time{}
	observation.Positions = sortedNativeDocumentPositions(observation.Positions)
	observation.Returns = slices.Clone(observation.Returns)
	for index := range observation.Returns {
		returned := &observation.Returns[index]
		returned.ObservedAt = time.Time{}
		returned.Positions = sortedNativeDocumentPositions(returned.Positions)
	}
	slices.SortFunc(observation.Returns, func(a, b orders.NativeReturnEvidence) int { return strings.Compare(a.ID, b.ID) })
	return ordersHash(observation)
}

func sortedNativeDocumentPositions(positions []orders.NativeDocumentPosition) []orders.NativeDocumentPosition {
	result := slices.Clone(positions)
	slices.SortFunc(result, func(a, b orders.NativeDocumentPosition) int { return strings.Compare(a.ID, b.ID) })
	return result
}
