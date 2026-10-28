-- +goose Up
CREATE TABLE moy_order_batches (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id text NOT NULL,
 source text NOT NULL DEFAULT 'moysklad' CHECK(source='moysklad'),
 account_id text NOT NULL,
 window_from timestamptz NOT NULL,
 window_to timestamptz NOT NULL CHECK(window_to > window_from),
 started_at timestamptz NOT NULL,
 finished_at timestamptz NOT NULL CHECK(finished_at >= started_at),
 traversal_status text NOT NULL CHECK(traversal_status='completed'),
 insertion_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
 FOREIGN KEY(tenant_id,source,account_id) REFERENCES stock_connections(tenant_id,source,account_id),
 UNIQUE(id,tenant_id,account_id)
);
CREATE INDEX moy_order_batches_scope_idx ON moy_order_batches(tenant_id,account_id,id DESC);
CREATE TABLE moy_order_identities (
 tenant_id text NOT NULL,
 source text NOT NULL DEFAULT 'moysklad' CHECK(source='moysklad'),
 account_id text NOT NULL,
 order_id text NOT NULL CHECK(length(trim(order_id)) > 0),
 PRIMARY KEY(tenant_id,account_id,order_id),
 FOREIGN KEY(tenant_id,source,account_id) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE TABLE moy_order_observations (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 batch_id bigint NOT NULL,
 tenant_id text NOT NULL,
 account_id text NOT NULL,
 order_id text NOT NULL,
 sync_id text,
 state_id text,
 warehouse_id text,
 source_created text,
 source_updated text,
 source_deleted text,
 source_moment text,
 applicable boolean,
 observed_at timestamptz NOT NULL,
 UNIQUE(batch_id,order_id),
 UNIQUE(id,tenant_id,account_id),
 FOREIGN KEY(batch_id,tenant_id,account_id) REFERENCES moy_order_batches(id,tenant_id,account_id),
 FOREIGN KEY(tenant_id,account_id,order_id) REFERENCES moy_order_identities(tenant_id,account_id,order_id)
);
CREATE UNIQUE INDEX moy_order_identity_case_idx ON moy_order_identities(tenant_id,account_id,lower(order_id));
CREATE INDEX moy_order_history_idx ON moy_order_observations(tenant_id,account_id,order_id,id);
CREATE TABLE moy_order_positions (
 observation_id bigint NOT NULL,
 tenant_id text NOT NULL,
 account_id text NOT NULL,
 position_id text NOT NULL CHECK(length(trim(position_id)) > 0),
 assortment_kind text,
 assortment_id text,
 quantity numeric,
 reserve numeric,
 shipped numeric,
 pack_id text,
 pack_unit_id text,
 pack_quantity numeric,
 observed_at timestamptz NOT NULL,
 CHECK((assortment_kind IS NULL) = (assortment_id IS NULL)),
 PRIMARY KEY(observation_id,position_id),
 FOREIGN KEY(observation_id,tenant_id,account_id) REFERENCES moy_order_observations(id,tenant_id,account_id)
);
CREATE UNIQUE INDEX moy_order_position_case_idx ON moy_order_positions(observation_id,lower(position_id));
CREATE INDEX moy_order_positions_item_idx ON moy_order_positions(tenant_id,account_id,assortment_kind,assortment_id,observation_id);
ALTER TABLE stock_decision_evidence ADD COLUMN customer_order_batch_id bigint;
ALTER TABLE stock_decision_evidence ADD CONSTRAINT stock_decision_order_batch_scope
 FOREIGN KEY(customer_order_batch_id,tenant_id,moysklad_account) REFERENCES moy_order_batches(id,tenant_id,account_id);

CREATE TRIGGER moy_order_batches_immutable BEFORE UPDATE OR DELETE ON moy_order_batches FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER moy_order_identities_immutable BEFORE UPDATE OR DELETE ON moy_order_identities FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER moy_order_observations_immutable BEFORE UPDATE OR DELETE ON moy_order_observations FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER moy_order_positions_immutable BEFORE UPDATE OR DELETE ON moy_order_positions FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- A completed observation cannot gain positions after the import commits.
-- +goose StatementBegin
CREATE FUNCTION moy_order_same_transaction() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE origin xid8;
BEGIN
 IF TG_TABLE_NAME='moy_order_observations' THEN
  SELECT insertion_xid INTO origin FROM moy_order_batches
   WHERE id=NEW.batch_id AND tenant_id=NEW.tenant_id AND account_id=NEW.account_id;
 ELSE
  SELECT b.insertion_xid INTO origin FROM moy_order_observations o
   JOIN moy_order_batches b ON b.id=o.batch_id AND b.tenant_id=o.tenant_id AND b.account_id=o.account_id
   WHERE o.id=NEW.observation_id AND o.tenant_id=NEW.tenant_id AND o.account_id=NEW.account_id;
 END IF;
 IF origin IS NOT NULL AND origin<>pg_current_xact_id() THEN
  RAISE EXCEPTION 'customer-order batch is sealed' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER moy_order_observation_sealed BEFORE INSERT ON moy_order_observations FOR EACH ROW EXECUTE FUNCTION moy_order_same_transaction();
CREATE TRIGGER moy_order_position_sealed BEFORE INSERT ON moy_order_positions FOR EACH ROW EXECUTE FUNCTION moy_order_same_transaction();

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM moy_order_batches) THEN
  RAISE EXCEPTION 'refusing to drop customer-order evidence history';
 END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE stock_decision_evidence DROP CONSTRAINT stock_decision_order_batch_scope;
ALTER TABLE stock_decision_evidence DROP COLUMN customer_order_batch_id;
DROP TABLE moy_order_positions;
DROP TABLE moy_order_observations;
DROP TABLE moy_order_identities;
DROP TABLE moy_order_batches;
DROP FUNCTION moy_order_same_transaction();
