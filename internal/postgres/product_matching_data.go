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
	After                           string
	HasNext                         bool
	Total                           int
}

func productMatchData(ctx context.Context, tx pgx.Tx, scope catalog.Scope) (productMatchSnapshot, error) {
	return productMatchDataPage(ctx, tx, scope, "")
}

func productMatchDataPage(ctx context.Context, tx pgx.Tx, scope catalog.Scope, after string) (productMatchSnapshot, error) {
	data := productMatchSnapshot{After: after}
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
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM ozon_product_observations WHERE import_id=$1`, data.OzonID).Scan(&data.Total); err != nil {
		return data, err
	}
	data.Sources, err = productMatchSources(ctx, tx, scope, data.OzonID, after)
	if len(data.Sources) > 100 {
		data.HasNext = true
		data.Sources = data.Sources[:100]
	}
	if err != nil {
		return data, err
	}
	data.Targets, err = productMatchTargets(ctx, tx, scope, data.MoyID, data.Sources)
	if err != nil {
		return data, err
	}
	data.Mappings, err = productMatchMappings(ctx, tx, scope, data.Sources, data.Targets)
	return data, err
}

func productMatchSources(ctx context.Context, tx pgx.Tx, scope catalog.Scope, importID int64, after string) ([]matching.Source, error) {
	rows, err := tx.Query(ctx, `SELECT p.product_id,p.offer_id,COALESCE(p.name,''),p.archived,EXISTS(SELECT 1 FROM ozon_product_observations d WHERE d.import_id=p.import_id AND product_matching_key(d.offer_id)=product_matching_key(p.offer_id) AND d.product_id<>p.product_id) FROM ozon_product_observations p WHERE p.import_id=$1 AND p.product_id>$2 ORDER BY p.product_id LIMIT 101`, importID, after)
	if err != nil {
		return nil, fmt.Errorf("read matching source products: %w", err)
	}
	defer rows.Close()
	result := make([]matching.Source, 0, 100)
	for rows.Next() {
		s := matching.Source{Match: domain.MatchSource{Identity: domain.ProductIdentity{System: "ozon", Account: scope.OzonAccount, Kind: "product"}}}
		if err = rows.Scan(&s.Match.Identity.ID, &s.Match.OfferID, &s.Name, &s.Archived, &s.DuplicateOffer); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}
func productMatchTargets(ctx context.Context, tx pgx.Tx, scope catalog.Scope, importID int64, sources []matching.Source) ([]matching.Target, error) {
	offers, names := make([]string, 0, len(sources)), make([]string, 0, len(sources))
	for _, s := range sources {
		offers = append(offers, s.Match.OfferID)
		names = append(names, s.Name)
	}
	rows, err := tx.Query(ctx, productMatchTargetsSQL, importID, offers, names)
	if err != nil {
		return nil, fmt.Errorf("read matching candidates: %w", err)
	}
	defer rows.Close()
	result := make([]matching.Target, 0, len(sources)*3)
	for rows.Next() {
		t := matching.Target{Match: domain.MatchTarget{Identity: domain.ProductIdentity{System: "moysklad", Account: scope.MoyskladAccount}}}
		if err = rows.Scan(&t.Match.Identity.Kind, &t.Match.Identity.ID, &t.Match.Article, &t.Match.Code, &t.Match.Name, &t.Match.ParentID, &t.Archived); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

const productMatchTargetsSQL = `WITH exact AS (
 SELECT t.kind,t.external_id,t.article,t.code,t.name,t.parent_id,t.archived FROM unnest($2::text[]) offer
 CROSS JOIN LATERAL(SELECT s.* FROM source_entities s WHERE s.import_id=$1 AND s.kind IN ('product','variant') AND product_matching_key(offer)<>'' AND (product_matching_key(s.article)=product_matching_key(offer) OR product_matching_key(s.code)=product_matching_key(offer)) ORDER BY s.kind,s.external_id LIMIT 3) t
), names AS (
 SELECT t.kind,t.external_id,t.article,t.code,t.name,t.parent_id,t.archived FROM unnest($3::text[]) title
 CROSS JOIN LATERAL(SELECT s.*, (SELECT count(*)::float FROM (SELECT unnest(product_matching_words(s.name)) INTERSECT SELECT unnest(product_matching_words(title))) shared) / NULLIF((SELECT count(*) FROM (SELECT unnest(product_matching_words(s.name)) UNION SELECT unnest(product_matching_words(title))) combined),0) score
 FROM (SELECT * FROM (SELECT s.* FROM source_entities s WHERE s.import_id=$1 AND s.kind IN ('product','variant') AND NOT COALESCE(s.archived,false) AND lower(s.name)=lower(title) ORDER BY s.kind,s.external_id LIMIT 3) exact_name
 UNION SELECT * FROM (SELECT s.* FROM source_entities s WHERE s.import_id=$1 AND s.kind IN ('product','variant') AND NOT COALESCE(s.archived,false) AND product_matching_words(s.name)&&product_matching_words(title) ORDER BY s.kind,s.external_id LIMIT 100) name_pool) s
 ORDER BY score DESC,s.kind,s.external_id LIMIT 3) t
) SELECT * FROM exact UNION SELECT * FROM names ORDER BY kind,external_id`

func productMatchMappings(ctx context.Context, tx pgx.Tx, scope catalog.Scope, sources []matching.Source, targets []matching.Target) ([]domain.Mapping, error) {
	ids, offers := make([]string, 0, len(sources)), make([]string, 0, len(sources))
	kinds, targetIDs := make([]string, 0, len(targets)), make([]string, 0, len(targets))
	for _, s := range sources {
		ids = append(ids, s.Match.Identity.ID)
		offers = append(offers, s.Match.OfferID)
	}
	for _, t := range targets {
		kinds = append(kinds, t.Match.Identity.Kind)
		targetIDs = append(targetIDs, t.Match.Identity.ID)
	}
	rows, err := tx.Query(ctx, productMatchMappingsSQL, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, ids, offers, kinds, targetIDs)
	if err != nil {
		return nil, fmt.Errorf("read matching associations: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Mapping, 0, len(sources))
	for rows.Next() {
		m := domain.Mapping{Source: domain.ProductIdentity{System: "ozon", Kind: "product"}, Target: domain.ProductIdentity{System: "moysklad"}}
		if err = rows.Scan(&m.Tenant, &m.Source.Account, &m.Source.ID, &m.OfferID, &m.Target.Account, &m.Target.Kind, &m.Target.ID, &m.Version, &m.Provenance, &m.ConfirmedAt); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

const productMatchMappingsSQL = `WITH relevant AS (
 SELECT m.* FROM product_mappings m WHERE m.tenant_id=$1 AND m.ozon_account=$3 AND m.ozon_product_id=ANY($4::text[])
 UNION SELECT m.* FROM unnest($5::text[]) offer CROSS JOIN LATERAL(SELECT m.* FROM product_mappings m WHERE m.tenant_id=$1 AND m.ozon_account=$3 AND m.ozon_offer_id=offer ORDER BY m.id LIMIT 2) m
 UNION SELECT m.* FROM unnest($6::text[],$7::text[]) t(kind,item) CROSS JOIN LATERAL(SELECT m.* FROM product_mappings m WHERE m.tenant_id=$1 AND m.moysklad_account=$2 AND m.target_kind=t.kind AND m.target_id=t.item ORDER BY m.id LIMIT 2) m
) SELECT tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance,confirmed_at FROM relevant ORDER BY id`

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
