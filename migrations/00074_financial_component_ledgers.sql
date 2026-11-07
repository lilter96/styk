-- +goose Up
CREATE TABLE orders_finance_ledgers (
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 report_year integer NOT NULL, report_month integer NOT NULL CHECK(report_month BETWEEN 1 AND 12),
 source_fingerprint text NOT NULL CHECK(source_fingerprint ~ '^[0-9a-f]{64}$'),
 ledger_version text NOT NULL CHECK(length(ledger_version) BETWEEN 1 AND 80),
 ledger jsonb NOT NULL CHECK(jsonb_typeof(ledger)='object' AND octet_length(ledger::text)<=16777216),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,report_year,report_month,source_fingerprint,ledger_version),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,report_year,report_month,source_fingerprint)
 REFERENCES orders_finance_observations(tenant_id,moysklad_account,ozon_account,report_year,report_month,fingerprint),
 CHECK((ledger->>'version'=ledger_version AND ledger->>'source_fingerprint'=source_fingerprint
 AND ledger#>>'{period,year}'=report_year::text AND ledger#>>'{period,month}'=report_month::text
 AND (ledger->>'rows_total')::integer=jsonb_array_length(ledger->'rows')
 AND (ledger->>'rows_classified')::integer=(ledger->>'rows_total')::integer
 AND (ledger->>'components_total')::integer=jsonb_array_length(ledger->'components')
 AND (ledger->>'components_total')::integer=(ledger->>'components_represented')::integer+(ledger->>'components_unrepresented')::integer
 AND (ledger->>'rows_total')::integer=(ledger->>'rows_mapped_to_native')::integer+(ledger->>'rows_partial')::integer+(ledger->>'rows_unmapped')::integer) IS TRUE),
 CHECK((ledger->>'complete')::boolean IS NOT TRUE OR ((ledger->>'components_unrepresented')::integer=0 AND (ledger->>'unknown_components')::integer=0 AND (ledger->>'rows_partial')::integer=0 AND (ledger->>'rows_unmapped')::integer=0))
 ,CHECK((ledger->>'rows_total')::integer>=0 AND (ledger->>'rows_mapped_to_native')::integer>=0 AND (ledger->>'rows_partial')::integer>=0 AND (ledger->>'rows_unmapped')::integer>=0 AND (ledger->>'components_represented')::integer>=0 AND (ledger->>'components_unrepresented')::integer>=0 AND (ledger->>'unknown_components')::integer>=0)
);
CREATE TRIGGER finance_ledgers_immutable BEFORE UPDATE OR DELETE ON orders_finance_ledgers FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_finance_ledgers) THEN
  RAISE EXCEPTION 'financial component ledger history prevents rollback; use a forward fix';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_finance_ledgers;
