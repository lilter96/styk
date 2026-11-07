package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

// Only a new pair of complete imported snapshots can trigger the standing rule.
// One worker turn confirms at most 100 pairs; the next turn handles the remainder.
func (s *Store) RunProductMatchAutomation(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var scope catalog.Scope
	err := s.Pool.QueryRow(ctx, productMatchDueSQL).Scan(&scope.Tenant, &scope.MoyskladAccount, &scope.OzonAccount)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find matching cycle: %w", err)
	}
	_, err = s.Pool.Exec(ctx, `UPDATE product_matching_policies SET last_checked_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, catalogScopeArgs(scope)...)
	if err != nil {
		return false, fmt.Errorf("schedule matching cycle: %w", err)
	}
	err = s.runProductMatchCycle(ctx, scope)
	if errors.Is(err, catalogsync.ErrStale) || errors.Is(err, catalogsync.ErrBlocked) {
		return false, nil
	}
	return err == nil, err
}

const productMatchDueSQL = `SELECT p.tenant_id,p.moysklad_account,p.ozon_account FROM product_matching_policies p
 JOIN LATERAL(SELECT i.id,i.finished_at FROM source_imports i WHERE i.source='moysklad' AND i.account_id=p.moysklad_account ORDER BY i.id DESC LIMIT 1) m ON true
 JOIN LATERAL(SELECT i.id,i.finished_at,b.traversal_status FROM source_imports i LEFT JOIN ozon_import_batches b ON b.import_id=i.id WHERE i.source='ozon' AND i.account_id=p.ozon_account ORDER BY i.id DESC LIMIT 1) o ON true
 WHERE p.policy->>'after_import'='true' AND p.policy->>'auto_confirm'='true'
 AND (p.last_checked_at<clock_timestamp()-interval '30 seconds' OR p.batch_cursor<>'')
 AND m.finished_at BETWEEN clock_timestamp()-interval '1 hour' AND clock_timestamp()
 AND o.finished_at BETWEEN clock_timestamp()-interval '1 hour' AND clock_timestamp() AND o.traversal_status='completed'
 AND (p.activated_moy_import<>m.id OR p.activated_ozon_import<>o.id)
 AND NOT EXISTS(SELECT 1 FROM product_matching_cycles c WHERE c.tenant_id=p.tenant_id AND c.moysklad_account=p.moysklad_account AND c.ozon_account=p.ozon_account AND c.revision=p.revision AND c.moy_import=m.id AND c.ozon_import=o.id)
 ORDER BY p.last_checked_at,p.tenant_id,p.moysklad_account,p.ozon_account LIMIT 1`

func (s *Store) runProductMatchCycle(ctx context.Context, scope catalog.Scope) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin matching cycle: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockReview(ctx, tx, MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}); err != nil {
		return err
	}
	p, err := readProductMatchPolicy(ctx, tx, scope)
	if err != nil {
		return err
	}
	if !p.AfterImport || !p.AutoConfirm || !p.ExactSKU {
		return catalogsync.ErrBlocked
	}
	if err = authorizeProductMatchCycle(ctx, tx, scope); err != nil {
		return err
	}
	data, err := productMatchData(ctx, tx, scope)
	if err != nil {
		return err
	}
	var after string
	if err = tx.QueryRow(ctx, `SELECT CASE WHEN batch_moy_import=$4 AND batch_ozon_import=$5 THEN batch_cursor ELSE '' END FROM product_matching_policies WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, append(catalogScopeArgs(scope), data.MoyID, data.OzonID)...).Scan(&after); err != nil {
		return err
	}
	if after != "" {
		data, err = productMatchDataPage(ctx, tx, scope, after)
		if err != nil {
			return err
		}
	}
	if err = productMatchCycleCurrent(ctx, tx, scope, p.Revision, data); err != nil {
		return err
	}
	actorHash, err := catalogHash(scope)
	if err != nil {
		return err
	}
	actor := fmt.Sprintf("matching-rule:%s:%d", actorHash[:32], p.Revision)
	preview, err := persistProductMatchPreview(ctx, tx, scope, p, data, actor)
	if err != nil {
		return err
	}
	if err = executeProductMatchCycle(ctx, tx, preview, data, actor); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit matching cycle: %w", err)
	}
	return nil
}

func productMatchCycleCurrent(ctx context.Context, tx pgx.Tx, scope catalog.Scope, revision int64, data productMatchSnapshot) error {
	var completed bool
	args := append(catalogScopeArgs(scope), revision, data.MoyID, data.OzonID)
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_matching_cycles WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND revision=$4 AND moy_import=$5 AND ozon_import=$6) OR EXISTS(SELECT 1 FROM product_matching_policies WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND activated_moy_import=$5 AND activated_ozon_import=$6)`, args...).Scan(&completed)
	if err != nil {
		return fmt.Errorf("check matching cycle evidence: %w", err)
	}
	if completed {
		return catalogsync.ErrStale
	}
	return nil
}

func executeProductMatchCycle(ctx context.Context, tx pgx.Tx, p ProductMatchPreview, data productMatchSnapshot, actor string) error {
	ids := make([]string, 0, 100)
	for _, item := range p.Items {
		if item.Status == "exact" {
			if len(ids) < 100 {
				ids = append(ids, item.ProductID)
			}
		}
	}
	key, err := catalogHash([]any{p.Scope, p.Policy.Revision, data.MoyID, data.OzonID, ids})
	if err != nil {
		return err
	}
	req := ProductMatchExecute{Scope: p.Scope, PreviewID: p.ID, Actor: actor, IdempotencyKey: key, Products: ids, Acknowledge: true}
	result, err := confirmProductMatchItems(ctx, tx, req, p)
	if err != nil {
		return err
	}
	if err = saveProductMatchRun(ctx, tx, req, result, key); err != nil {
		return err
	}
	if data.HasNext {
		_, err = tx.Exec(ctx, `UPDATE product_matching_policies SET batch_cursor=$4,batch_moy_import=$5,batch_ozon_import=$6 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, p.Scope.Tenant, p.Scope.MoyskladAccount, p.Scope.OzonAccount, data.Sources[len(data.Sources)-1].Match.Identity.ID, data.MoyID, data.OzonID)
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_matching_cycles(tenant_id,moysklad_account,ozon_account,revision,moy_import,ozon_import,run_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, p.Scope.Tenant, p.Scope.MoyskladAccount, p.Scope.OzonAccount, p.Policy.Revision, data.MoyID, data.OzonID, result.ID)
	if err != nil {
		return fmt.Errorf("finish matching cycle: %w", err)
	}
	return nil
}

func authorizeProductMatchCycle(ctx context.Context, tx pgx.Tx, scope catalog.Scope) error {
	var owner string
	if err := tx.QueryRow(ctx, `SELECT owner_user_id FROM product_matching_policies WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 FOR SHARE`, catalogScopeArgs(scope)...).Scan(&owner); err != nil {
		return err
	}
	if err := productMatchOwnerCurrent(ctx, tx, scope, owner); err != nil {
		return err
	}
	return nil
}
