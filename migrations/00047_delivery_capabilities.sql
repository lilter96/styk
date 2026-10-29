-- +goose Up
-- Observed facts only: this table grants no stock or provider transition right.
-- Warehouse and delivery-method IDs deliberately occupy different columns.
CREATE TABLE delivery_capability_observations (
 tenant_id text NOT NULL,
 ozon_account text NOT NULL,
 provider text GENERATED ALWAYS AS ('ozon'::text) STORED,
 warehouse_id text NOT NULL CHECK(length(warehouse_id) BETWEEN 1 AND 256),
 delivery_method_id text NOT NULL DEFAULT '' CHECK(length(delivery_method_id)<=256),
 revision bigint NOT NULL CHECK(revision>0),
 id text NOT NULL CHECK(id ~ '^[0-9a-f]{64}$'),
 contract_hash text NOT NULL CHECK(contract_hash ~ '^[0-9a-f]{64}$'),
 contract_checked_at timestamptz NOT NULL,
 profile jsonb NOT NULL CHECK(jsonb_typeof(profile)='object' AND octet_length(profile::text)<=32768),
 observed_at timestamptz NOT NULL,
 checked_at timestamptz NOT NULL CHECK(checked_at>=observed_at),
 valid_until timestamptz NOT NULL CHECK(valid_until>checked_at AND valid_until<=checked_at+interval '5 minutes'),
 PRIMARY KEY(tenant_id,ozon_account,warehouse_id,delivery_method_id,revision),
 FOREIGN KEY(tenant_id,provider,ozon_account) REFERENCES stock_connections(tenant_id,source,account_id),
 CHECK((profile->'scope'=jsonb_build_object('tenant',tenant_id,'ozon_account',ozon_account)
  AND profile->>'warehouse_id'=warehouse_id AND COALESCE(profile#>>'{method_id,value}','')=delivery_method_id
  AND profile->>'id'=id AND profile->>'revision'=revision::text
  AND profile#>>'{contract,hash}'=contract_hash
  AND (profile#>>'{contract,checked_at}')::timestamptz=contract_checked_at
  AND (profile->>'observed_at')::timestamptz=observed_at
  AND (profile->>'checked_at')::timestamptz=checked_at
  AND (profile->>'valid_until')::timestamptz=valid_until) IS TRUE)
);
CREATE INDEX delivery_capability_latest ON delivery_capability_observations(tenant_id,ozon_account,warehouse_id,delivery_method_id,checked_at DESC);

-- +goose Down
-- Observation history must be deliberately retained/exported before rollback.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM delivery_capability_observations) THEN
  RAISE EXCEPTION 'refusing to discard delivery capability observation history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE delivery_capability_observations;
