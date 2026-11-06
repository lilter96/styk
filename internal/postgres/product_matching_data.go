package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/matching"
)

type productMatchSnapshot struct {
	MoyID, OzonID                   int64
	MoyFingerprint, OzonFingerprint string
	Sources                         []matching.Source
	Targets                         []matching.Target
	Mappings                        []domain.Mapping
}

func productMatchData(ctx context.Context, tx pgx.Tx, scope catalog.Scope) (productMatchSnapshot, error) {
	var data productMatchSnapshot
	var fresh bool
	err := tx.QueryRow(ctx, `WITH imports AS (
 SELECT DISTINCT ON(i.source) i.source,i.id,i.fingerprint,i.finished_at,b.traversal_status
 FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
 LEFT JOIN ozon_import_batches b ON b.import_id=i.id
 WHERE ((i.source='moysklad' AND i.account_id=$2) OR (i.source='ozon' AND i.account_id=$3)) AND (c.include_import_history OR i.started_at>=c.registered_at)
 ORDER BY i.source,i.id DESC)
 SELECT COALESCE(max(id) FILTER(WHERE source='moysklad'),0),COALESCE(max(id) FILTER(WHERE source='ozon'),0),COALESCE(max(fingerprint) FILTER(WHERE source='moysklad'),''),COALESCE(max(fingerprint) FILTER(WHERE source='ozon'),''),count(*)=2 AND COALESCE(bool_and(COALESCE(finished_at BETWEEN clock_timestamp()-interval '1 hour' AND clock_timestamp() AND (source='moysklad' OR traversal_status='completed'),false)),false) FROM imports`, catalogScopeArgs(scope)...).Scan(&data.MoyID, &data.OzonID, &data.MoyFingerprint, &data.OzonFingerprint, &fresh)
	if err != nil {
		return data, fmt.Errorf("read matching imports: %w", err)
	}
	if !fresh {
		return data, catalogsync.ErrStale
	}
	data.Sources, err = productMatchSources(ctx, tx, scope, data.OzonID)
	if err != nil {
		return data, err
	}
	data.Targets, err = productMatchTargets(ctx, tx, scope, data.MoyID)
	if err != nil {
		return data, err
	}
	data.Mappings, err = productMatchMappings(ctx, tx, scope)
	return data, err
}

func productMatchSources(ctx context.Context, tx pgx.Tx, scope catalog.Scope, importID int64) ([]matching.Source, error) {
	rows, err := tx.Query(ctx, `SELECT product_id,offer_id,COALESCE(name,''),archived FROM ozon_product_observations WHERE import_id=$1 ORDER BY product_id LIMIT 2001`, importID)
	if err != nil {
		return nil, fmt.Errorf("read matching source products: %w", err)
	}
	defer rows.Close()
	result := make([]matching.Source, 0, 100)
	for rows.Next() {
		s := matching.Source{Match: domain.MatchSource{Identity: domain.ProductIdentity{System: "ozon", Account: scope.OzonAccount, Kind: "product"}}}
		if err = rows.Scan(&s.Match.Identity.ID, &s.Match.OfferID, &s.Name, &s.Archived); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	if len(result) > 2000 {
		return nil, catalogsync.ErrBlocked
	}
	return result, rows.Err()
}
func productMatchTargets(ctx context.Context, tx pgx.Tx, scope catalog.Scope, importID int64) ([]matching.Target, error) {
	rows, err := tx.Query(ctx, `SELECT kind,external_id,article,code,name,parent_id,archived FROM source_entities WHERE import_id=$1 AND kind IN ('product','variant') ORDER BY kind,external_id LIMIT 2001`, importID)
	if err != nil {
		return nil, fmt.Errorf("read matching target products: %w", err)
	}
	defer rows.Close()
	result := make([]matching.Target, 0, 100)
	for rows.Next() {
		t := matching.Target{Match: domain.MatchTarget{Identity: domain.ProductIdentity{System: "moysklad", Account: scope.MoyskladAccount}}}
		if err = rows.Scan(&t.Match.Identity.Kind, &t.Match.Identity.ID, &t.Match.Article, &t.Match.Code, &t.Match.Name, &t.Match.ParentID, &t.Archived); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	if len(result) > 2000 {
		return nil, catalogsync.ErrBlocked
	}
	return result, rows.Err()
}
func productMatchMappings(ctx context.Context, tx pgx.Tx, scope catalog.Scope) ([]domain.Mapping, error) {
	rows, err := tx.Query(ctx, `SELECT tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance,confirmed_at FROM product_mappings WHERE tenant_id=$1 AND (ozon_account=$3 OR moysklad_account=$2) ORDER BY id LIMIT 5001`, catalogScopeArgs(scope)...)
	if err != nil {
		return nil, fmt.Errorf("read matching associations: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Mapping, 0, 100)
	for rows.Next() {
		m := domain.Mapping{Source: domain.ProductIdentity{System: "ozon", Kind: "product"}, Target: domain.ProductIdentity{System: "moysklad"}}
		if err = rows.Scan(&m.Tenant, &m.Source.Account, &m.Source.ID, &m.OfferID, &m.Target.Account, &m.Target.Kind, &m.Target.ID, &m.Version, &m.Provenance, &m.ConfirmedAt); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	if len(result) > 5000 {
		return nil, catalogsync.ErrBlocked
	}
	return result, rows.Err()
}

func (s *Store) ProductMatchRun(ctx context.Context, scope catalog.Scope, id string) (ProductMatchRun, error) {
	var result ProductMatchRun
	if scope.Validate() != nil {
		return result, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	if _, err := readCatalogConfig(ctx, s.Pool, scope); err != nil {
		return result, err
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT result FROM product_matching_runs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4`, append(catalogScopeArgs(scope), id)...).Scan(&raw)
	if err != nil {
		return result, catalogError("read matching result", err)
	}
	return result, catalogDecode(raw, &result)
}
