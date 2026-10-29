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
		rows, e := tx.Query(ctx, `WITH latest AS (SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' JOIN stock_connections c ON c.source=i.source AND c.account_id=i.account_id AND c.tenant_id=$1 WHERE i.source='ozon' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1) SELECT p.product_id,p.offer_id,p.sku,p.archived,CASE WHEN m.moysklad_account=$3 THEN m.target_kind END,CASE WHEN m.moysklad_account=$3 THEN m.target_id END FROM ozon_product_observations p JOIN latest l ON l.id=p.import_id LEFT JOIN product_mappings m ON m.tenant_id=$1 AND m.ozon_account=$2 AND m.ozon_product_id=p.product_id WHERE length($3::text)>0 AND ($4='' OR p.offer_id ILIKE '%'||$4||'%' OR p.product_id ILIKE '%'||$4||'%' OR p.sku ILIKE '%'||$4||'%') AND ($5='' OR p.archived::text=$5) AND ($7='' OR ($7='confirmed' AND m.moysklad_account=$3) OR ($7='unconfirmed' AND (m.ozon_product_id IS NULL OR m.moysklad_account<>$3))) ORDER BY p.product_id LIMIT 51 OFFSET $6`, tenant, account, moy, search, archived, offset, mappingFilter)
		if e != nil {
			writeError(w, 503, "PRODUCTS_UNAVAILABLE")
			return
		}
		defer rows.Close()
		type item struct {
			ProductID      string      `json:"product_id"`
			OfferID        string      `json:"offer_id"`
			SKU            *string     `json:"sku"`
			Archived       *bool       `json:"archived"`
			TargetKind     *string     `json:"target_kind"`
			TargetID       *string     `json:"target_id"`
			Status         string      `json:"match_status"`
			Candidates     []candidate `json:"candidates"`
			CandidateCount int         `json:"candidate_count"`
			Truncated      bool        `json:"candidates_truncated"`
		}
		items := []item{}
		for rows.Next() {
			var v item
			if rows.Scan(&v.ProductID, &v.OfferID, &v.SKU, &v.Archived, &v.TargetKind, &v.TargetID) != nil {
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
		targets, mappings, available, truncated, e := matchingData(ctx, tx, tenant, account, moy)
		if e != nil {
			writeError(w, 503, "MATCHING_UNAVAILABLE")
			return
		}
		engine := matching.New(targets, mappings)
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
				v.Candidates = append(v.Candidates, candidate{Kind: c.Target.Kind, ID: c.Target.ID, Evidence: fields})
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

type candidate struct {
	Kind     string   `json:"kind"`
	ID       string   `json:"id"`
	Evidence []string `json:"evidence"`
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

// Load a bounded candidate universe once per page. If the bound is exceeded,
// no unique-match claim is made; the UI is told that evidence was truncated.
func matchingData(ctx context.Context, tx pgx.Tx, tenant, account, moy string) ([]domain.MatchTarget, []domain.Mapping, bool, bool, error) {
	var importID int64
	e := tx.QueryRow(ctx, `SELECT i.id FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1`, tenant, moy).Scan(&importID)
	if e == pgx.ErrNoRows {
		return nil, nil, false, false, nil
	}
	if e != nil {
		return nil, nil, false, false, e
	}
	rows, e := tx.Query(ctx, `SELECT kind,external_id,article,code FROM source_entities WHERE import_id=$1 AND kind IN ('product','variant') ORDER BY kind,external_id LIMIT 2001`, importID)
	if e != nil {
		return nil, nil, false, false, e
	}
	targets := []domain.MatchTarget{}
	for rows.Next() {
		var v domain.MatchTarget
		v.Identity = domain.ProductIdentity{System: "moysklad", Account: moy}
		if e = rows.Scan(&v.Identity.Kind, &v.Identity.ID, &v.Article, &v.Code); e != nil {
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
	rows, e = tx.Query(ctx, `SELECT tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,confirmed_at FROM product_mappings WHERE tenant_id=$1 AND (ozon_account=$2 OR moysklad_account=$3) ORDER BY id LIMIT 2001`, tenant, account, moy)
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
