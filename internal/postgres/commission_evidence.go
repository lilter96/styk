package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func persistFinancialEvidence(ctx context.Context, tx pgx.Tx, source orders.RealizationReport) error {
	if source.Evidence == nil {
		return nil
	}
	record := *source.Evidence
	if record.Validate() != nil || record.Scope != source.Scope || record.Period != source.Period || record.SourceFingerprint != source.Fingerprint {
		return orders.ErrInvalid
	}
	var revision int64
	err := tx.QueryRow(ctx, `SELECT revision FROM connection_credentials WHERE tenant_id=$1 AND provider='ozon' AND account_id=$2 AND enabled`, record.Scope.Tenant, record.Scope.OzonAccount).Scan(&revision)
	if err != nil {
		return ordersError("verify financial evidence credential revision", err)
	}
	if revision != record.CredentialRevision {
		return orders.ErrStale
	}
	args := append(ordersScopeArgs(record.Scope), record.Period.Year, record.Period.Month, record.CredentialRevision, record.SourceFingerprint, record.RawDigest, record.BodyBytes, record.Chunks, record.ObservedAt)
	_, err = tx.Exec(ctx, `INSERT INTO orders_finance_raw_evidence(tenant_id,moysklad_account,ozon_account,report_year,report_month,credential_revision,source_fingerprint,raw_digest,body_bytes,ciphertext,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(tenant_id,moysklad_account,ozon_account,report_year,report_month,credential_revision,source_fingerprint,raw_digest) DO NOTHING`, args...)
	return ordersRowsError("archive encrypted financial source evidence", err)
}

func (s *Store) FinancialEvidence(ctx context.Context, scope orders.Scope, period orders.RealizationPeriod, revision int64, fingerprint, digest string) (orders.FinancialEvidenceRecord, error) {
	record := orders.FinancialEvidenceRecord{Scope: scope, Period: period, CredentialRevision: revision, SourceFingerprint: fingerprint, RawDigest: digest}
	if scope.Validate() != nil || period.Month < 1 || period.Month > 12 || revision < 1 || len(fingerprint) != 64 || len(digest) != 64 {
		return record, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return record, ordersError("begin encrypted financial evidence read", err)
	}
	defer rollbackMerchantMutation(tx)
	config, err := readOrderConfig(ctx, tx, scope)
	if err != nil {
		return record, err
	}
	if err = validateOrderReadAuthority(ctx, tx, config); err != nil {
		return record, err
	}
	args := append(ordersScopeArgs(scope), period.Year, period.Month, revision, fingerprint, digest)
	err = tx.QueryRow(ctx, `SELECT body_bytes,ciphertext,observed_at FROM orders_finance_raw_evidence WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND report_year=$4 AND report_month=$5 AND credential_revision=$6 AND source_fingerprint=$7 AND raw_digest=$8`, args...).Scan(&record.BodyBytes, &record.Chunks, &record.ObservedAt)
	if err != nil {
		return record, ordersError("read exact encrypted financial source evidence", err)
	}
	if err = record.Validate(); err != nil {
		return record, err
	}
	return record, ordersRowsError("finish encrypted financial evidence read", tx.Commit(ctx))
}
