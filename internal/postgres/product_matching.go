package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/matching"
)

func readProductMatchPolicy(ctx context.Context, q catalogQuerier, scope catalog.Scope) (matching.Policy, error) {
	if _, err := readCatalogConfig(ctx, q, scope); err != nil {
		return matching.Policy{}, err
	}
	var raw []byte
	p := matching.Policy{ExactSKU: true, Names: true}
	err := q.QueryRow(ctx, `SELECT policy FROM product_matching_policies WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, catalogScopeArgs(scope)...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, fmt.Errorf("load matching policy: %w", err)
	}
	return p, catalogDecode(raw, &p)
}
func (s *Store) ProductMatchPolicy(ctx context.Context, scope catalog.Scope) (matching.Policy, error) {
	if scope.Validate() != nil {
		return matching.Policy{}, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readProductMatchPolicy(ctx, s.Pool, scope)
}
func (s *Store) SaveProductMatchPolicy(ctx context.Context, scope catalog.Scope, p matching.Policy, actor string) (matching.Policy, error) {
	if !p.Criterion.Resolved().Valid() || scope.Validate() != nil || p.Revision < 0 || p.AutoConfirm && !p.ExactSKU || p.AfterImport && !p.AutoConfirm {
		return p, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer rollbackMerchantMutation(tx)
	if err = lockProductMatch(ctx, tx, scope, actor); err != nil {
		return p, err
	}
	previous, err := readProductMatchPolicy(ctx, tx, scope)
	if err != nil {
		return p, err
	}
	if p.Revision != previous.Revision {
		return p, catalogsync.ErrStale
	}
	p.Revision++
	if err = saveProductMatchPolicy(ctx, tx, scope, p, actor); err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}
func lockProductMatch(ctx context.Context, tx pgx.Tx, scope catalog.Scope, actor string) error {
	if err := catalogActor(ctx, tx, scope.Tenant, actor); err != nil {
		return err
	}
	return lockReview(ctx, tx, MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount})
}
