package console

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/matching"
	"github.com/lilter96/styk/internal/postgres"
)

func products(db *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tenant := strings.TrimSpace(q.Get("tenant"))
		account := strings.TrimSpace(q.Get("ozon_account"))
		moy := strings.TrimSpace(q.Get("moysklad_account"))
		if tenant == "" || account == "" || moy == "" || len(tenant) > 256 || len(account) > 256 || len(moy) > 256 {
			writeError(w, 400, "INVALID_SCOPE")
			return
		}
		offset := 0
		if s := q.Get("offset"); s != "" {
			v, e := strconv.Atoi(s)
			if e != nil || v < 0 || v > 100000 {
				writeError(w, 400, "INVALID_PAGE")
				return
			}
			offset = v
		}
		search := q.Get("search")
		if len(search) > 100 {
			writeError(w, 400, "INVALID_SEARCH")
			return
		}
		archived := q.Get("archived")
		if archived != "" && archived != "true" && archived != "false" {
			writeError(w, 400, "INVALID_FILTER")
			return
		}
		mappingFilter := q.Get("mapping")
		if mappingFilter != "" && mappingFilter != "confirmed" && mappingFilter != "unconfirmed" {
			writeError(w, 400, "INVALID_FILTER")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), db.Timeout)
		defer cancel()
		tx, e := db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if e != nil {
			writeError(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		defer tx.Rollback(context.Background())
		var valid bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='ozon' AND account_id=$2) AND EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$3)`, tenant, account, moy).Scan(&valid); e != nil {
			writeError(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		if !valid {
			writeError(w, 404, "SCOPE_NOT_REGISTERED")
			return
		}
		rows, e := tx.Query(ctx, productPageQuery, tenant, account, moy, search, archived, offset, mappingFilter, matchTrimChars)
		if e != nil {
			writeError(w, 503, "PRODUCTS_UNAVAILABLE")
			return
		}
		defer rows.Close()
		type item struct {
			ProductID      string      `json:"product_id"`
			OfferID        string      `json:"offer_id"`
			Name           *string     `json:"name,omitempty"`
			SKU            *string     `json:"sku"`
			Archived       *bool       `json:"archived"`
			TargetKind     *string     `json:"target_kind"`
			TargetID       *string     `json:"target_id"`
			TargetName     *string     `json:"target_name,omitempty"`
			TargetArticle  *string     `json:"target_article,omitempty"`
			Status         string      `json:"match_status"`
			Candidates     []candidate `json:"candidates"`
			CandidateCount int         `json:"candidate_count"`
			Truncated      bool        `json:"candidates_truncated"`
		}
		items := []item{}
		for rows.Next() {
			var v item
			if rows.Scan(&v.ProductID, &v.OfferID, &v.SKU, &v.Archived, &v.TargetKind, &v.TargetID, &v.Name, &v.TargetName, &v.TargetArticle) != nil {
				writeError(w, 503, "PRODUCTS_UNAVAILABLE")
				return
			}
			items = append(items, v)
		}
		if rows.Err() != nil {
			writeError(w, 503, "PRODUCTS_UNAVAILABLE")
			return
		}
		rows.Close()
		hasNext := len(items) > 50
		if hasNext {
			items = items[:50]
		}
		offers, productIDs := []string{}, []string{}
		for _, item := range items {
			offers, productIDs = append(offers, item.OfferID), append(productIDs, item.ProductID)
		}
		targets, mappings, available, truncated, e := matchingData(ctx, tx, tenant, account, moy, offers, productIDs)
		if e != nil {
			writeError(w, 503, "MATCHING_UNAVAILABLE")
			return
		}
		engine := matching.New(targets, mappings)
		targetDetails := map[domain.ProductIdentity]domain.MatchTarget{}
		for _, target := range targets {
			targetDetails[target.Identity] = target
		}
		for i := range items {
			v := &items[i]
			v.Candidates = []candidate{}
			if !available || truncated {
				v.Status = "INSUFFICIENT_DATA"
				v.Truncated = truncated
				continue
			}
			result := engine.Match(tenant, domain.MatchSource{Identity: domain.ProductIdentity{System: "ozon", Account: account, Kind: "product", ID: v.ProductID}, OfferID: v.OfferID}, moy)
			v.Status = presentMatch(result.Status)
			v.CandidateCount = len(result.Candidates)
			for _, c := range result.Candidates {
				if len(v.Candidates) == 20 {
					v.Truncated = true
					break
				}
				fields := []string{}
				for _, e := range c.Evidence {
					fields = append(fields, e.TargetField)
				}
				detail := targetDetails[c.Target]
				v.Candidates = append(v.Candidates, candidate{Kind: c.Target.Kind, ID: c.Target.ID, Evidence: fields, Name: detail.Name, Article: detail.Article, Code: detail.Code, ParentID: detail.ParentID})
			}
		}
		if e = tx.Commit(ctx); e != nil {
			writeError(w, 503, "PRODUCTS_UNAVAILABLE")
			return
		}
		writeJSON(w, struct {
			Items   []item `json:"items"`
			HasNext bool   `json:"has_next"`
			Offset  int    `json:"offset"`
		}{items, hasNext, offset})
	}
}

// Materialize searched source records once per request. The membership tests
// retain both confirmed-target and normalized candidate search semantics without
// rescanning the source catalog for every Ozon product.
const productPageQuery = `
WITH latest AS (
  SELECT i.id FROM source_imports i
  JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed'
  JOIN stock_connections c ON c.source=i.source AND c.account_id=i.account_id AND c.tenant_id=$1
  WHERE i.source='ozon' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at)
  ORDER BY i.id DESC LIMIT 1
), source_latest AS (
  SELECT i.id FROM source_imports i
  JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
  WHERE i.source='moysklad' AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at)
  ORDER BY i.id DESC LIMIT 1
), searched_targets AS MATERIALIZED (
  SELECT s.kind,s.external_id,btrim(s.article,$8) AS article,btrim(s.code,$8) AS code
  FROM source_entities s JOIN source_latest sl ON sl.id=s.import_id
  WHERE $4<>'' AND s.kind IN ('product','variant')
    AND (s.name ILIKE '%'||$4||'%' OR s.article ILIKE '%'||$4||'%' OR s.code ILIKE '%'||$4||'%')
), page AS (
  SELECT p.product_id,p.offer_id,p.sku,p.archived,
    CASE WHEN m.moysklad_account=$3 THEN m.target_kind END AS target_kind,
    CASE WHEN m.moysklad_account=$3 THEN m.target_id END AS target_id,NULLIF(p.name,'') AS name
  FROM ozon_product_observations p JOIN latest l ON l.id=p.import_id
  LEFT JOIN product_mappings m ON m.tenant_id=$1 AND m.ozon_account=$2 AND m.ozon_product_id=p.product_id
  WHERE length($3::text)>0 AND (
    $4='' OR p.offer_id ILIKE '%'||$4||'%' OR p.product_id ILIKE '%'||$4||'%' OR p.sku ILIKE '%'||$4||'%'
    OR p.name ILIKE '%'||$4||'%'
    OR (m.moysklad_account=$3 AND (m.target_kind,m.target_id) IN (SELECT kind,external_id FROM searched_targets))
    OR (btrim(p.offer_id,$8)<>'' AND (
      btrim(p.offer_id,$8) IN (SELECT article FROM searched_targets)
      OR btrim(p.offer_id,$8) IN (SELECT code FROM searched_targets)))
  ) AND ($5='' OR p.archived::text=$5)
    AND ($7='' OR ($7='confirmed' AND m.moysklad_account=$3)
      OR ($7='unconfirmed' AND (m.ozon_product_id IS NULL OR m.moysklad_account<>$3)))
  ORDER BY p.product_id LIMIT 51 OFFSET $6
)
SELECT page.product_id,page.offer_id,page.sku,page.archived,page.target_kind,page.target_id,page.name,NULLIF(mapped.name,''),NULLIF(mapped.article,'')
FROM page LEFT JOIN source_entities mapped ON mapped.import_id=(SELECT id FROM source_latest)
  AND mapped.kind=page.target_kind AND mapped.external_id=page.target_id
ORDER BY page.product_id`

type candidate struct {
	Kind     string   `json:"kind"`
	ID       string   `json:"id"`
	Evidence []string `json:"evidence"`
	Name     string   `json:"name,omitempty"`
	Article  string   `json:"article,omitempty"`
	Code     string   `json:"code,omitempty"`
	ParentID string   `json:"parent_id,omitempty"`
}

func presentMatch(status string) string {
	switch status {
	case matching.NoCandidate:
		return "NO_CANDIDATE"
	case matching.UniqueCandidate:
		return "ONE_CANDIDATE"
	case matching.MultipleCandidates:
		return "AMBIGUOUS"
	case matching.AlreadyConfirmed:
		return "CONFIRMED"
	case matching.MappingConflict:
		return "CONFLICT"
	default:
		return "INSUFFICIENT_DATA"
	}
}

// The same Unicode white-space set as strings.TrimSpace. Only identifiers for
// this page are loaded, so large unrelated catalogs do not destroy candidate
// suggestions. A bound overflow still refuses a uniqueness claim.
const matchTrimChars = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

func matchingData(ctx context.Context, tx pgx.Tx, tenant, account, moy string, offers, products []string) ([]domain.MatchTarget, []domain.Mapping, bool, bool, error) {
	var importID int64
	e := tx.QueryRow(ctx, `SELECT i.id FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1`, tenant, moy).Scan(&importID)
	if e == pgx.ErrNoRows {
		return nil, nil, false, false, nil
	}
	if e != nil {
		return nil, nil, false, false, e
	}
	normalized := make([]string, 0, len(offers))
	for _, offer := range offers {
		if v := matching.Normalize(offer); v != "" {
			normalized = append(normalized, v)
		}
	}
	rows, e := tx.Query(ctx, `SELECT kind,external_id,article,code,name,parent_id FROM source_entities WHERE import_id=$1 AND kind IN ('product','variant') AND (btrim(article,$3)=ANY($2::text[]) OR btrim(code,$3)=ANY($2::text[])) ORDER BY kind,external_id LIMIT 2001`, importID, normalized, matchTrimChars)
	if e != nil {
		return nil, nil, false, false, e
	}
	targets := []domain.MatchTarget{}
	for rows.Next() {
		var v domain.MatchTarget
		v.Identity = domain.ProductIdentity{System: "moysklad", Account: moy}
		if e = rows.Scan(&v.Identity.Kind, &v.Identity.ID, &v.Article, &v.Code, &v.Name, &v.ParentID); e != nil {
			rows.Close()
			return nil, nil, false, false, e
		}
		targets = append(targets, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, nil, false, false, e
	}
	if len(targets) > 2000 {
		return nil, nil, true, true, nil
	}
	kinds, ids := []string{}, []string{}
	for _, target := range targets {
		kinds, ids = append(kinds, target.Identity.Kind), append(ids, target.Identity.ID)
	}
	rows, e = tx.Query(ctx, `SELECT m.tenant_id,m.ozon_account,m.ozon_product_id,m.ozon_offer_id,m.moysklad_account,m.target_kind,m.target_id,m.version,m.confirmed_at FROM product_mappings m WHERE m.tenant_id=$1 AND ((m.ozon_account=$2 AND (m.ozon_product_id=ANY($4::text[]) OR m.ozon_offer_id=ANY($5::text[]))) OR (m.moysklad_account=$3 AND EXISTS(SELECT 1 FROM unnest($6::text[],$7::text[]) AS wanted(kind,id) WHERE wanted.kind=m.target_kind AND wanted.id=m.target_id)) OR EXISTS(SELECT 1 FROM product_mappings current WHERE current.tenant_id=m.tenant_id AND current.ozon_account=$2 AND current.ozon_product_id=ANY($4::text[]) AND current.moysklad_account=m.moysklad_account AND current.target_kind=m.target_kind AND current.target_id=m.target_id)) ORDER BY m.id LIMIT 2001`, tenant, account, moy, products, offers, kinds, ids)
	if e != nil {
		return nil, nil, false, false, e
	}
	mappings := []domain.Mapping{}
	for rows.Next() {
		var m domain.Mapping
		m.Source.System = "ozon"
		m.Source.Kind = "product"
		m.Target.System = "moysklad"
		if e = rows.Scan(&m.Tenant, &m.Source.Account, &m.Source.ID, &m.OfferID, &m.Target.Account, &m.Target.Kind, &m.Target.ID, &m.Version, &m.ConfirmedAt); e != nil {
			rows.Close()
			return nil, nil, false, false, e
		}
		mappings = append(mappings, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, nil, false, false, e
	}
	if len(mappings) > 2000 {
		return nil, nil, true, true, nil
	}
	return targets, mappings, true, false, nil
}
