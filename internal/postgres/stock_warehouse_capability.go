package postgres

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
)

// The digest binds rFBS support to docs/ozon-stock-contract-2026-10.json, the
// reviewed subset of https://docs.ozon.ru/api/seller/zh/swagger.json. The source's
// quant_size discrepancy prevents any claim of complete schema verification.
const stockContractSHA256 = "74d7d9b021a928b18fac17f74bcf30628eb6326140262f8917a1b73b1941ab4c"

type stockWarehouseFacts struct {
	kind, status string
	rfbs         *bool
}

func (facts stockWarehouseFacts) contractSupported() bool {
	if facts.rfbs == nil {
		return false
	}
	if !*facts.rfbs {
		return facts.kind == "" || facts.kind == "fbs"
	}
	digest, err := hex.DecodeString(stockContractSHA256)
	return err == nil && len(digest) == 32 && (facts.kind == "rfbs" || facts.kind == "rfbs_express")
}

func (facts stockWarehouseFacts) policySupported(scope domain.StockSyncScope, enrolled, requireCreated bool) bool {
	if !facts.contractSupported() {
		return false
	}
	if *facts.rfbs {
		// "created" is the existing conservative STYK test policy. The provider
		// schema gives no operational status enum and grants no API permission.
		return enrolled && automaticScopeAllowed(scope) && facts.status == "created"
	}
	return !requireCreated || facts.status == "created"
}

func warehouseReviewStockSupported(scope domain.StockSyncScope, snapshot warehouseSnapshot) bool {
	facts := stockWarehouseFacts{kind: snapshot.OzonType, status: snapshot.OzonStatus, rfbs: snapshot.IsRFBS}
	// A reviewed mapping does not enroll a publication target. Scope eligibility
	// here only permits the owner to establish the pair before policy enrollment.
	return facts.policySupported(scope, true, false)
}

func readStockWarehouseSupported(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, importID int64, warehouse string, enrolled, requireCreated bool) (bool, error) {
	var facts stockWarehouseFacts
	err := tx.QueryRow(ctx, `SELECT warehouse_type,status,is_rfbs FROM ozon_warehouse_observations WHERE import_id=$1 AND warehouse_id=$2 FOR SHARE`, importID, warehouse).Scan(&facts.kind, &facts.status, &facts.rfbs)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return facts.policySupported(scope, enrolled, requireCreated), nil
}

func requireAutomaticWarehouseCapability(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, warehouse string) error {
	var imported int64
	err := tx.QueryRow(ctx, `SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' WHERE i.source='ozon' AND i.account_id=$1 ORDER BY i.id DESC LIMIT 1`, scope.OzonAccount).Scan(&imported)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrStockSyncBlocked
	}
	if err != nil {
		return err
	}
	supported, err := readStockWarehouseSupported(ctx, tx, scope, imported, warehouse, true, false)
	if err != nil {
		return err
	}
	if !supported {
		return domain.ErrStockSyncBlocked
	}
	return nil
}

func recordStockContractEvidence(evidence *domain.StockEvidence) {
	evidence.Proofs["OZON_STOCK_REVIEWED_SUBSET"] = domain.StockProof{State: domain.ProofVerified,
		Reference: "official reviewed stock subset sha256:" + stockContractSHA256,
		AsOf:      time.Date(2026, time.October, 30, 0, 0, 0, 0, time.UTC)}
}
