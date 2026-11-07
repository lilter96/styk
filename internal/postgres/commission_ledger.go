package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func persistFinancialLedger(ctx context.Context, tx pgx.Tx, source orders.RealizationReport) error {
	ledger, err := ordermoney.ClassifyRealization(source)
	if err != nil {
		return err
	}
	raw, err := ordersJSON(ledger)
	if err != nil {
		return err
	}
	args := append(ordersScopeArgs(source.Scope), source.Period.Year, source.Period.Month, source.Fingerprint, ledger.Version, raw)
	_, err = tx.Exec(ctx, `INSERT INTO orders_finance_ledgers(tenant_id,moysklad_account,ozon_account,report_year,report_month,source_fingerprint,ledger_version,ledger) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(tenant_id,moysklad_account,ozon_account,report_year,report_month,source_fingerprint,ledger_version) DO NOTHING`, args...)
	if err != nil {
		return ordersError("retain immutable financial component ledger", err)
	}
	var identical bool
	err = tx.QueryRow(ctx, `SELECT ledger=$8::jsonb FROM orders_finance_ledgers WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND report_year=$4 AND report_month=$5 AND source_fingerprint=$6 AND ledger_version=$7`, args...).Scan(&identical)
	if err != nil {
		return ordersError("verify immutable financial classification version", err)
	}
	if !identical {
		return orders.ErrConflict
	}
	return nil
}
