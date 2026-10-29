package console

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/postgres"
)

type warehouseEvidence struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Account    string    `json:"account"`
	Status     string    `json:"status"`
	IsRFBS     *bool     `json:"is_rfbs"`
	ObservedAt time.Time `json:"observed_at"`
	StockRows  int64     `json:"stock_rows"`
}
type warehouseReviewMapping struct {
	ID          int64     `json:"mapping_id"`
	Version     int       `json:"version"`
	MoyID       string    `json:"moysklad_warehouse_id"`
	OzonID      string    `json:"ozon_warehouse_id"`
	Provenance  string    `json:"provenance"`
	ConfirmedAt time.Time `json:"confirmed_at"`
}

// warehouseReview returns a local database view; selection never writes a mapping.
func warehouseReview(db *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, moy, ozon, _, offset, ok := reviewParams(r)
		moyID, ozonID := strings.TrimSpace(r.URL.Query().Get("moysklad_warehouse")), strings.TrimSpace(r.URL.Query().Get("ozon_warehouse"))
		if !ok || moyID == "" || ozonID == "" || len(moyID) > 256 || len(ozonID) > 256 {
			writeError(w, 400, "INVALID_SCOPE")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), db.Timeout)
		defer cancel()
		tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			writeError(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		defer tx.Rollback(context.Background())
		var valid bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$2) AND EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='ozon' AND account_id=$3)`, tenant, moy, ozon).Scan(&valid)
		if err != nil {
			writeError(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		if !valid {
			writeError(w, 404, "SCOPE_NOT_REGISTERED")
			return
		}
		var ms, oz *warehouseEvidence
		a := warehouseEvidence{ID: moyID, Account: moy}
		err = tx.QueryRow(ctx, `WITH latest AS (SELECT i.id,i.finished_at FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1) SELECT e.name,l.finished_at,(SELECT count(*) FROM source_warehouse_stock_observations s WHERE s.import_id=l.id AND s.warehouse_id=e.external_id) FROM latest l JOIN source_entities e ON e.import_id=l.id AND e.kind='store' AND e.external_id=$3`, tenant, moy, moyID).Scan(&a.Name, &a.ObservedAt, &a.StockRows)
		if err == nil {
			ms = &a
		} else if err != pgx.ErrNoRows {
			writeError(w, 503, "WAREHOUSE_REVIEW_UNAVAILABLE")
			return
		}
		b := warehouseEvidence{ID: ozonID, Account: ozon}
		err = tx.QueryRow(ctx, `WITH latest AS (SELECT i.id,i.finished_at FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='ozon' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1) SELECT e.name,e.status,e.is_rfbs,l.finished_at,(SELECT count(*) FROM ozon_fbs_stock_observations s WHERE s.import_id=l.id AND s.warehouse_id=e.warehouse_id) FROM latest l JOIN ozon_warehouse_observations e ON e.import_id=l.id AND e.warehouse_id=$3`, tenant, ozon, ozonID).Scan(&b.Name, &b.Status, &b.IsRFBS, &b.ObservedAt, &b.StockRows)
		if err == nil {
			oz = &b
		} else if err != pgx.ErrNoRows {
			writeError(w, 503, "WAREHOUSE_REVIEW_UNAVAILABLE")
			return
		}
		rows, err := tx.Query(ctx, `SELECT m.id,m.version,m.moysklad_warehouse_id,m.ozon_warehouse_id,m.provenance,m.confirmed_at,EXISTS(SELECT 1 FROM warehouse_mapping_history h JOIN stock_connections mc ON mc.tenant_id=h.tenant_id AND mc.source='moysklad' AND mc.account_id=h.moysklad_account JOIN stock_connections oc ON oc.tenant_id=h.tenant_id AND oc.source='ozon' AND oc.account_id=h.ozon_account JOIN source_imports mi ON mi.id=h.moysklad_import_id AND mi.source='moysklad' AND mi.account_id=h.moysklad_account JOIN source_imports oi ON oi.id=h.ozon_import_id AND oi.source='ozon' AND oi.account_id=h.ozon_account WHERE h.mapping_id=m.id AND h.version=m.version AND h.tenant_id=m.tenant_id AND h.moysklad_account=m.moysklad_account AND h.ozon_account=m.ozon_account AND h.moysklad_warehouse_id=m.moysklad_warehouse_id AND h.ozon_warehouse_id=m.ozon_warehouse_id AND h.status=m.status AND h.provenance=m.provenance AND h.confirmed_at=m.confirmed_at AND (mc.include_import_history OR (mi.started_at>=mc.registered_at AND h.confirmed_at>=mc.registered_at)) AND (oc.include_import_history OR (oi.started_at>=oc.registered_at AND h.confirmed_at>=oc.registered_at))) FROM warehouse_mappings m WHERE m.tenant_id=$1 AND m.moysklad_account=$2 AND m.ozon_account=$3 AND m.status='active' AND (m.moysklad_warehouse_id=$4 OR m.ozon_warehouse_id=$5) ORDER BY m.id LIMIT 3`, tenant, moy, ozon, moyID, ozonID)
		if err != nil {
			writeError(w, 503, "WAREHOUSE_REVIEW_UNAVAILABLE")
			return
		}
		mappings := []warehouseReviewMapping{}
		hiddenMapping := false
		for rows.Next() {
			var v warehouseReviewMapping
			var visible bool
			if rows.Scan(&v.ID, &v.Version, &v.MoyID, &v.OzonID, &v.Provenance, &v.ConfirmedAt, &visible) != nil {
				rows.Close()
				writeError(w, 503, "WAREHOUSE_REVIEW_UNAVAILABLE")
				return
			}
			if visible {
				mappings = append(mappings, v)
			} else {
				hiddenMapping = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			writeError(w, 503, "WAREHOUSE_REVIEW_UNAVAILABLE")
			return
		}
		status := "ELIGIBLE_FOR_REVIEW"
		if ms == nil || oz == nil || (oz != nil && oz.IsRFBS == nil) {
			status = "MISSING_SOURCE_EVIDENCE"
		} else if *oz.IsRFBS {
			status = "UNSUPPORTED_WAREHOUSE"
		}
		if hiddenMapping {
			status = "MAPPING_CONFLICT"
		}
		for _, m := range mappings {
			if m.MoyID != moyID || m.OzonID != ozonID {
				status = "MAPPING_CONFLICT"
				break
			}
			if status == "ELIGIBLE_FOR_REVIEW" {
				status = "ALREADY_MAPPED"
			}
		}
		rows, err = tx.Query(ctx, `SELECT h.mapping_id,h.version,h.moysklad_warehouse_id,h.ozon_warehouse_id,h.provenance,h.confirmed_at FROM warehouse_mapping_history h JOIN stock_connections mc ON mc.tenant_id=h.tenant_id AND mc.source='moysklad' AND mc.account_id=h.moysklad_account JOIN stock_connections oc ON oc.tenant_id=h.tenant_id AND oc.source='ozon' AND oc.account_id=h.ozon_account JOIN source_imports mi ON mi.id=h.moysklad_import_id AND mi.source='moysklad' AND mi.account_id=h.moysklad_account JOIN source_imports oi ON oi.id=h.ozon_import_id AND oi.source='ozon' AND oi.account_id=h.ozon_account WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3 AND (h.moysklad_warehouse_id=$4 OR h.ozon_warehouse_id=$5) AND (mc.include_import_history OR (mi.started_at>=mc.registered_at AND h.confirmed_at>=mc.registered_at)) AND (oc.include_import_history OR (oi.started_at>=oc.registered_at AND h.confirmed_at>=oc.registered_at)) ORDER BY h.mapping_id,h.version LIMIT 51 OFFSET $6`, tenant, moy, ozon, moyID, ozonID, offset)
		if err != nil {
			writeError(w, 503, "WAREHOUSE_REVIEW_UNAVAILABLE")
			return
		}
		history := []warehouseReviewMapping{}
		for rows.Next() {
			var v warehouseReviewMapping
			if rows.Scan(&v.ID, &v.Version, &v.MoyID, &v.OzonID, &v.Provenance, &v.ConfirmedAt) != nil {
				rows.Close()
				writeError(w, 503, "WAREHOUSE_REVIEW_UNAVAILABLE")
				return
			}
			history = append(history, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			writeError(w, 503, "WAREHOUSE_REVIEW_UNAVAILABLE")
			return
		}
		more := len(history) > 50
		if more {
			history = history[:50]
		}
		if tx.Commit(ctx) != nil {
			writeError(w, 503, "WAREHOUSE_REVIEW_UNAVAILABLE")
			return
		}
		writeJSON(w, struct {
			Status   string                   `json:"status"`
			Moy      *warehouseEvidence       `json:"moysklad"`
			Ozon     *warehouseEvidence       `json:"ozon"`
			Mappings []warehouseReviewMapping `json:"mappings"`
			History  []warehouseReviewMapping `json:"history"`
			HasNext  bool                     `json:"has_next"`
			Offset   int                      `json:"offset"`
		}{status, ms, oz, mappings, history, more, offset})
	}
}
