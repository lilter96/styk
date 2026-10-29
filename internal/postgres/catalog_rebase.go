package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalogsync"
)

func rebaseCatalogSiblings(ctx context.Context, tx pgx.Tx, applied catalogsync.Work, c catalogsync.Completion, now time.Time) error {
	rows, err := tx.Query(ctx, `SELECT work FROM catalog_sync_work WHERE operation_id=$1 AND target_key=$2 AND id<>$3 AND state='QUEUED' ORDER BY id FOR UPDATE`, applied.OperationID, applied.EnrollmentID, applied.ID)
	if err != nil {
		return fmt.Errorf("lock catalog sibling revision guards: %w", err)
	}
	siblings := make([]catalogsync.Work, 0, 1)
	for rows.Next() {
		var raw []byte
		var w catalogsync.Work
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return fmt.Errorf("scan catalog sibling: %w", err)
		}
		if err = catalogDecode(raw, &w); err != nil {
			rows.Close()
			return err
		}
		siblings = append(siblings, w)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return fmt.Errorf("read catalog siblings: %w", err)
	}
	for _, w := range siblings {
		if err = prepareCatalogSibling(ctx, tx, applied, c, w, now); err != nil {
			return err
		}
	}
	return nil
}

func prepareCatalogSibling(ctx context.Context, tx pgx.Tx, applied catalogsync.Work, c catalogsync.Completion, w catalogsync.Work, now time.Time) error {
	prepared, err := catalogsync.RebaseWork(w, applied.Command, *c.ObservedSource, *c.ObservedTarget)
	if errors.Is(err, catalogsync.ErrStale) {
		return blockCatalogSibling(ctx, tx, w, now)
	}
	if err != nil {
		return err
	}
	raw, err := catalogJSON(prepared)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_rebases(work_id,applied_work_id,operation_id,tenant_id,moysklad_account,ozon_account,prepared_work,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, w.ID, applied.ID, w.OperationID, w.Scope.Tenant, w.Scope.MoyskladAccount, w.Scope.OzonAccount, raw, now); err != nil {
		return fmt.Errorf("journal verified catalog sibling revisions: %w", err)
	}
	if err = writeCatalogWork(ctx, tx, prepared, now); err != nil {
		return err
	}
	return nil
}

func blockCatalogSibling(ctx context.Context, tx pgx.Tx, w catalogsync.Work, now time.Time) error {
	w.State = catalogsync.StateBlocked
	w.Code = "SIBLING_FACTS_CHANGED"
	if err := writeCatalogWork(ctx, tx, w, now); err != nil {
		return err
	}
	if err := finishCatalogTask(ctx, tx, w, now); err != nil {
		return err
	}
	return nil
}
