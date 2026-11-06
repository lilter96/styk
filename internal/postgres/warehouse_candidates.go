package postgres

import (
	"context"
	"encoding/json"
	"fmt"
)

type WarehouseCandidate struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

type WarehouseCandidatePage struct {
	Items   []WarehouseCandidate `json:"items"`
	Total   int                  `json:"total"`
	HasNext bool                 `json:"has_next"`
	Offset  int                  `json:"offset"`
}

func (s *Store) WarehouseCandidates(ctx context.Context, scope MatchScope, request WarehouseReadRequest) (WarehouseCandidatePage, error) {
	var result WarehouseCandidatePage
	if !validWarehouseScope(scope) || !request.Valid() {
		return result, ErrReviewEvidence
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var registered bool
	var raw []byte
	err := s.Pool.QueryRow(ctx, warehouseCandidatesSQL, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, request.Search, request.Offset, request.Limit).Scan(&registered, &raw)
	if err != nil {
		return result, fmt.Errorf("read warehouse candidates: %w", err)
	}
	if !registered {
		return result, ErrReviewScope
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("decode warehouse candidates: %w", err)
	}
	return result, nil
}

const warehouseCandidatesSQL = warehouseReadCTE + `, filtered AS (
 SELECT mw.external_id AS id,mw.name, m.id IS NULL AS available FROM mw LEFT JOIN m ON m.moysklad_warehouse_id=mw.external_id
 WHERE $4='' OR strpos(lower(mw.name),lower($4))>0 OR strpos(lower(mw.external_id),lower($4))>0
), page AS (SELECT * FROM filtered ORDER BY id LIMIT $6 OFFSET $5)
 SELECT (SELECT registered FROM connections),jsonb_build_object('items',coalesce((SELECT jsonb_agg(to_jsonb(page) ORDER BY id) FROM page),'[]'::jsonb),'total',(SELECT count(*) FROM filtered),'has_next',(SELECT count(*) FROM filtered)>$5+$6,'offset',$5)`
