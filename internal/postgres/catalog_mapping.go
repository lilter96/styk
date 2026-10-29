package postgres

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

func catalogMappings(ctx context.Context, q catalogQuerier, scope catalog.Scope, targets []catalogsync.Enrollment) (map[string]catalog.Mapping, error) {
	ids, err := catalogMappingIDs(targets)
	if err != nil {
		return nil, err
	}
	result := make(map[string]catalog.Mapping, len(targets))
	if len(ids) == 0 {
		return result, nil
	}
	args := append(catalogScopeArgs(scope), ids)
	rows, err := q.Query(ctx, `SELECT m.id,m.version,m.target_kind,m.target_id,m.ozon_product_id,m.ozon_offer_id FROM product_mappings m JOIN product_mapping_history h ON h.mapping_id=m.id AND h.version=m.version AND h.tenant_id=m.tenant_id AND h.moysklad_account=m.moysklad_account AND h.ozon_account=m.ozon_account AND h.target_kind=m.target_kind AND h.target_id=m.target_id AND h.ozon_product_id=m.ozon_product_id AND h.ozon_offer_id=m.ozon_offer_id JOIN stock_connections mc ON mc.tenant_id=m.tenant_id AND mc.source='moysklad' AND mc.account_id=m.moysklad_account JOIN stock_connections oc ON oc.tenant_id=m.tenant_id AND oc.source='ozon' AND oc.account_id=m.ozon_account JOIN source_imports mi ON mi.id=h.moysklad_import_id AND mi.source='moysklad' AND mi.account_id=m.moysklad_account JOIN source_imports oi ON oi.id=h.ozon_import_id AND oi.source='ozon' AND oi.account_id=m.ozon_account WHERE m.tenant_id=$1 AND m.moysklad_account=$2 AND m.ozon_account=$3 AND m.id=ANY($4) AND (mc.include_import_history OR (mi.started_at>=mc.registered_at AND m.confirmed_at>=mc.registered_at)) AND (oc.include_import_history OR (oi.started_at>=oc.registered_at AND m.confirmed_at>=oc.registered_at))`, args...)
	if err != nil {
		return nil, fmt.Errorf("load catalog mapping revisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var m catalog.Mapping
		m.Source.Account = catalog.Account{Provider: catalog.ProviderMoySklad, ID: scope.MoyskladAccount}
		m.Target.Account = catalog.Account{Provider: catalog.ProviderOzon, ID: scope.OzonAccount}
		m.Target.EntityType = "product"
		m.Confirmed = true
		if err = rows.Scan(&id, &m.Version, &m.Source.EntityType, &m.Source.ID, &m.Target.ID, &m.Target.OfferID); err != nil {
			return nil, fmt.Errorf("scan catalog mapping: %w", err)
		}
		m.ID = strconv.FormatInt(id, 10)
		result[m.ID] = m
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read catalog mappings: %w", err)
	}
	if err = validateCatalogMappingSet(targets, result); err != nil {
		return nil, err
	}
	return result, nil
}

func validateCatalogMappingSet(targets []catalogsync.Enrollment, result map[string]catalog.Mapping) error {
	for _, e := range targets {
		if e.Create != nil {
			continue
		}
		m, ok := result[e.MappingID]
		if !ok || !catalogEnrollmentMatches(e, m) {
			return catalogsync.ErrStale
		}
	}
	return nil
}

func catalogMappingIDs(targets []catalogsync.Enrollment) ([]int64, error) {
	ids := make([]int64, 0, len(targets))
	for _, e := range targets {
		if e.Create != nil {
			continue
		}
		id, err := strconv.ParseInt(e.MappingID, 10, 64)
		if err != nil || id < 1 {
			return nil, catalogsync.ErrInvalid
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func catalogEnrollmentMatches(e catalogsync.Enrollment, m catalog.Mapping) bool {
	if e.MappingVersion != m.Version {
		return false
	}
	if e.Source.Account.Provider == catalog.ProviderOzon {
		m.Source, m.Target = m.Target, m.Source
	}
	return e.Source.SameEntity(m.Source) && e.Target.SameEntity(m.Target)
}

func validateCatalogCreations(ctx context.Context, q catalogQuerier, scope catalog.Scope, targets []catalogsync.Enrollment) error {
	providers := make([]string, 0, len(targets))
	kinds := make([]string, 0, len(targets))
	ids := make([]string, 0, len(targets))
	for _, e := range targets {
		if e.Create != nil {
			providers = append(providers, string(e.Source.Account.Provider))
			kinds = append(kinds, e.Source.EntityType)
			ids = append(ids, e.Source.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var existing bool
	args := append(catalogScopeArgs(scope), providers, kinds, ids)
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM unnest($4::text[],$5::text[],$6::text[]) AS e(provider,kind,id) JOIN product_mappings m ON m.tenant_id=$1 AND m.moysklad_account=$2 AND m.ozon_account=$3 AND ((e.provider='moysklad' AND (e.kind='' OR m.target_kind=e.kind) AND m.target_id=e.id) OR (e.provider='ozon' AND m.ozon_product_id=e.id)))`, args...).Scan(&existing)
	if err != nil {
		return fmt.Errorf("check catalog creation associations: %w", err)
	}
	if existing {
		return catalogsync.ErrStale
	}
	return nil
}

func saveCatalogEnrollment(ctx context.Context, tx pgx.Tx, c catalogsync.Config, e catalogsync.Enrollment, m catalog.Mapping) error {
	var mappingID, mappingVersion, kind, itemID, productID, offerID any
	if e.Create == nil {
		id, err := strconv.ParseInt(m.ID, 10, 64)
		if err != nil {
			return fmt.Errorf("parse confirmed catalog mapping: %w", err)
		}
		mappingID, mappingVersion, kind, itemID, productID, offerID = id, m.Version, m.Source.EntityType, m.Source.ID, m.Target.ID, m.Target.OfferID
	}
	args := append(catalogScopeArgs(c.Scope), e.ID, e.Source.Account.Provider, e.Source.Account.ID, e.Source.EntityType, e.Source.ID, e.Target.Account.Provider, e.Target.Account.ID, e.Target.EntityType, e.Target.ID, e.Target.OfferID, mappingID, mappingVersion, kind, itemID, productID, offerID, e.Create != nil)
	tag, err := tx.Exec(ctx, `INSERT INTO catalog_sync_associations(tenant_id,moysklad_account,ozon_account,target_key,source_provider,source_account,source_kind,source_id,target_provider,target_account,target_kind,target_id,target_offer_id,mapping_id,mapping_version,mapping_kind,mapping_item_id,mapping_product_id,mapping_offer_id,provisional) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20) ON CONFLICT(tenant_id,moysklad_account,ozon_account,target_key) DO NOTHING`, args...)
	if err != nil {
		return catalogError("save enrolled catalog association", err)
	}
	if tag.RowsAffected() == 0 {
		if err = checkCatalogAssociation(ctx, tx, c.Scope, e); err != nil {
			return err
		}
	}
	raw, err := catalogJSON(e)
	if err != nil {
		return err
	}
	args = append(catalogScopeArgs(c.Scope), c.Revision, e.ID, raw)
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_enrollments(tenant_id,moysklad_account,ozon_account,config_revision,target_key,enrollment) VALUES($1,$2,$3,$4,$5,$6)`, args...); err != nil {
		return fmt.Errorf("save catalog enrollment revision: %w", err)
	}
	return nil
}

func checkCatalogAssociation(ctx context.Context, tx pgx.Tx, scope catalog.Scope, e catalogsync.Enrollment) error {
	var source, target catalog.Identity
	var provisional bool
	var mappingID string
	var version int64
	args := append(catalogScopeArgs(scope), e.ID)
	err := tx.QueryRow(ctx, `SELECT source_provider,source_account,source_kind,source_id,target_provider,target_account,target_kind,target_id,target_offer_id,provisional,COALESCE(mapping_id::text,''),COALESCE(mapping_version,0) FROM catalog_sync_associations WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND target_key=$4`, args...).Scan(&source.Account.Provider, &source.Account.ID, &source.EntityType, &source.ID, &target.Account.Provider, &target.Account.ID, &target.EntityType, &target.ID, &target.OfferID, &provisional, &mappingID, &version)
	if err != nil {
		return fmt.Errorf("check enrolled catalog association: %w", err)
	}
	if source.Account != e.Source.Account || source.EntityType != e.Source.EntityType || source.ID != e.Source.ID || (target.Account != e.Target.Account || target.EntityType != e.Target.EntityType || target.ID != e.Target.ID || target.OfferID != e.Target.OfferID) || provisional != (e.Create != nil) || mappingID != e.MappingID || version != e.MappingVersion {
		return catalogsync.ErrConflict
	}
	return nil
}
