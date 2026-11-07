package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/matching"
)

func saveProductMatchPolicy(ctx context.Context, tx pgx.Tx, scope catalog.Scope, p matching.Policy, actor string) error {
	raw, err := catalogJSON(p)
	if err != nil {
		return err
	}
	owner := ""
	if strings.HasPrefix(actor, "user.") {
		if err = tx.QueryRow(ctx, `SELECT user_id FROM saas_sessions WHERE audit_id=$1`, actor).Scan(&owner); err != nil {
			return fmt.Errorf("read matching policy owner: %w", err)
		}
	}
	cursor := catalogCursor{}
	if err = pinCatalogImports(ctx, tx, scope, &cursor); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_matching_policies(tenant_id,moysklad_account,ozon_account,revision,policy,actor,owner_user_id,activated_moy_import,activated_ozon_import)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET revision=EXCLUDED.revision,policy=EXCLUDED.policy,actor=EXCLUDED.actor,owner_user_id=EXCLUDED.owner_user_id,activated_moy_import=EXCLUDED.activated_moy_import,activated_ozon_import=EXCLUDED.activated_ozon_import,updated_at=clock_timestamp()`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, p.Revision, raw, actor, owner, cursor.MoyskladImport, cursor.OzonImport)
	if err != nil {
		return fmt.Errorf("save matching policy: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_matching_policy_audits(tenant_id,moysklad_account,ozon_account,revision,policy,actor,owner_user_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, p.Revision, raw, actor, owner)
	if err != nil {
		return fmt.Errorf("audit matching policy: %w", err)
	}
	return nil
}

func persistProductMatchPreview(ctx context.Context, tx pgx.Tx, scope catalog.Scope, policy matching.Policy, data productMatchSnapshot, actor string) (ProductMatchPreview, error) {
	p := ProductMatchPreview{Scope: scope, Policy: policy, After: data.After, HasNext: data.HasNext, Total: data.Total, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	var err error
	p.Fingerprint, err = catalogHash(data)
	if err != nil {
		return p, err
	}
	p.ID, err = catalogID()
	if err != nil {
		return p, err
	}
	p.Items = matching.Preview(scope.Tenant, scope.MoyskladAccount, policy, data.Sources, data.Targets, data.Mappings)
	raw, err := catalogJSON(p)
	if err != nil {
		return p, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_matching_previews(id,tenant_id,moysklad_account,ozon_account,actor,preview) VALUES($1,$2,$3,$4,$5,$6)`, p.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, actor, raw)
	if err != nil {
		return p, fmt.Errorf("save matching preview: %w", err)
	}
	return p, nil
}

func productMatchOwnerCurrent(ctx context.Context, tx pgx.Tx, scope catalog.Scope, owner string) error {
	if owner != "" {
		if err := ordersOwnerCurrent(ctx, tx, scope.Tenant, owner); err != nil {
			return fmt.Errorf("matching policy owner unavailable: %w", catalogsync.ErrBlocked)
		}
		return nil
	}
	var local bool
	if err := tx.QueryRow(ctx, `SELECT legacy_unclaimed FROM saas_workspaces WHERE id=$1 FOR SHARE`, scope.Tenant).Scan(&local); err != nil {
		return fmt.Errorf("read matching workspace owner: %w", err)
	}
	if !local {
		return catalogsync.ErrBlocked
	}
	return nil
}
