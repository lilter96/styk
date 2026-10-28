package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"example.com/styk/internal/domain"
	"example.com/styk/internal/matching"
	"github.com/jackc/pgx/v5"
)

type MatchScope struct{ Tenant, OzonAccount, MoyskladAccount string }
type MatchData struct {
	OzonImportID, MoyskladImportID int64
	Sources                        []domain.MatchSource
	Targets                        []domain.MatchTarget
	Mappings                       []domain.Mapping
}

func (s MatchScope) Validate() error {
	if strings.TrimSpace(s.Tenant) == "" || strings.TrimSpace(s.OzonAccount) == "" || strings.TrimSpace(s.MoyskladAccount) == "" {
		return errors.New("tenant and both account IDs are required")
	}
	return nil
}
func loadMatchData(ctx context.Context, tx pgx.Tx, scope MatchScope) (MatchData, error) {
	var d MatchData
	err := tx.QueryRow(ctx, `SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' WHERE i.source='ozon' AND i.account_id=$1 ORDER BY i.id DESC LIMIT 1`, scope.OzonAccount).Scan(&d.OzonImportID)
	if err != nil {
		return d, fmt.Errorf("latest completed Ozon import: %w", err)
	}
	err = tx.QueryRow(ctx, `SELECT id FROM source_imports WHERE source='moysklad' AND account_id=$1 ORDER BY id DESC LIMIT 1`, scope.MoyskladAccount).Scan(&d.MoyskladImportID)
	if err != nil {
		return d, fmt.Errorf("latest completed MoySklad import: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT product_id,offer_id FROM ozon_product_observations WHERE import_id=$1 ORDER BY product_id`, d.OzonImportID)
	if err != nil {
		return d, fmt.Errorf("load Ozon products: %w", err)
	}
	for rows.Next() {
		var v domain.MatchSource
		v.Identity = domain.ProductIdentity{System: "ozon", Account: scope.OzonAccount, Kind: "product"}
		if err = rows.Scan(&v.Identity.ID, &v.OfferID); err != nil {
			rows.Close()
			return d, err
		}
		d.Sources = append(d.Sources, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	rows, err = tx.Query(ctx, `SELECT kind,external_id,article,code FROM source_entities WHERE import_id=$1 AND kind IN ('product','variant') ORDER BY kind,external_id`, d.MoyskladImportID)
	if err != nil {
		return d, fmt.Errorf("load MoySklad products: %w", err)
	}
	for rows.Next() {
		var v domain.MatchTarget
		v.Identity.System = "moysklad"
		v.Identity.Account = scope.MoyskladAccount
		if err = rows.Scan(&v.Identity.Kind, &v.Identity.ID, &v.Article, &v.Code); err != nil {
			rows.Close()
			return d, err
		}
		d.Targets = append(d.Targets, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	rows, err = tx.Query(ctx, `SELECT tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance,confirmed_at FROM product_mappings WHERE tenant_id=$1 AND (ozon_account=$2 OR moysklad_account=$3)`, scope.Tenant, scope.OzonAccount, scope.MoyskladAccount)
	if err != nil {
		return d, fmt.Errorf("load confirmed mappings: %w", err)
	}
	for rows.Next() {
		var m domain.Mapping
		m.Source.System = "ozon"
		m.Source.Kind = "product"
		m.Target.System = "moysklad"
		if err = rows.Scan(&m.Tenant, &m.Source.Account, &m.Source.ID, &m.OfferID, &m.Target.Account, &m.Target.Kind, &m.Target.ID, &m.Version, &m.Provenance, &m.ConfirmedAt); err != nil {
			rows.Close()
			return d, err
		}
		d.Mappings = append(d.Mappings, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	return d, nil
}
func (s *Store) MatchData(ctx context.Context, scope MatchScope) (MatchData, error) {
	if err := scope.Validate(); err != nil {
		return MatchData{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return MatchData{}, err
	}
	defer tx.Rollback(context.Background())
	d, err := loadMatchData(ctx, tx, scope)
	if err != nil {
		return d, err
	}
	if err = tx.Commit(ctx); err != nil {
		return d, err
	}
	return d, nil
}

// ConfirmMatch rechecks the latest imports and candidate while holding the import and mapping locks.
func (s *Store) ConfirmMatch(ctx context.Context, scope MatchScope, productID, kind, targetID, provenance string) (domain.MatchResult, error) {
	if err := scope.Validate(); err != nil {
		return domain.MatchResult{}, err
	}
	if strings.TrimSpace(productID) == "" || strings.TrimSpace(targetID) == "" || strings.TrimSpace(provenance) == "" || (kind != "product" && kind != "variant") {
		return domain.MatchResult{}, errors.New("source, target kind, target ID and provenance are required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.MatchResult{}, err
	}
	defer tx.Rollback(context.Background())
	for _, key := range []string{"ozon:" + scope.OzonAccount, "moysklad:" + scope.MoyskladAccount, "matching:" + scope.Tenant} {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return domain.MatchResult{}, fmt.Errorf("lock matching scope: %w", err)
		}
	}
	d, err := loadMatchData(ctx, tx, scope)
	if err != nil {
		return domain.MatchResult{}, err
	}
	var src *domain.MatchSource
	for i := range d.Sources {
		if d.Sources[i].Identity.ID == productID {
			src = &d.Sources[i]
			break
		}
	}
	if src == nil {
		return domain.MatchResult{}, errors.New("source product not in latest completed import")
	}
	result := matching.New(d.Targets, d.Mappings).Match(scope.Tenant, *src, scope.MoyskladAccount)
	target := domain.ProductIdentity{System: "moysklad", Account: scope.MoyskladAccount, Kind: kind, ID: targetID}
	if result.Confirmed != nil {
		if result.Confirmed.Target != target {
			return result, errors.New("existing confirmed mapping conflicts with requested target")
		}
		if err = tx.Commit(ctx); err != nil {
			return result, err
		}
		return result, nil
	}
	if result.Status == matching.MappingConflict {
		return result, errors.New("existing confirmed mapping conflicts with this offer")
	}
	found := false
	for _, c := range result.Candidates {
		if c.Target == target {
			found = true
			break
		}
	}
	if !found {
		return result, errors.New("target is not a current matching candidate")
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8) RETURNING id`, scope.Tenant, scope.OzonAccount, productID, src.OfferID, scope.MoyskladAccount, kind, targetID, provenance).Scan(&id)
	if err != nil {
		return result, fmt.Errorf("insert confirmed mapping: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_mapping_history(mapping_id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,ozon_import_id,moysklad_import_id) SELECT id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,$2,$3 FROM product_mappings WHERE id=$1`, id, d.OzonImportID, d.MoyskladImportID)
	if err != nil {
		return result, fmt.Errorf("insert mapping history: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("commit confirmation: %w", err)
	}
	return result, nil
}
