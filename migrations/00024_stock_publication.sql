-- +goose Up
-- Publication is separate from the read-only import queue. A dispatched target
-- is never reclaimed for another write; uncertain outcomes require readback.
CREATE TABLE stock_sync_reviews (
 id text PRIMARY KEY CHECK(length(id)=32),
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 ready boolean NOT NULL,
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 CHECK(expires_at>created_at)
);
CREATE INDEX stock_sync_reviews_scope_idx ON stock_sync_reviews(tenant_id,moysklad_account,ozon_account,created_at DESC);
CREATE TABLE stock_sync_settings (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 enabled boolean NOT NULL DEFAULT false,
 review_id text REFERENCES stock_sync_reviews(id),
 updated_at timestamptz NOT NULL DEFAULT now(),
 last_evaluation_key text NOT NULL DEFAULT '',
 last_proof_check_at timestamptz NOT NULL DEFAULT 'epoch',
 last_checks jsonb NOT NULL DEFAULT '[]'::jsonb,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account),
 moysklad_source text NOT NULL DEFAULT 'moysklad' CHECK(moysklad_source='moysklad'),
 ozon_source text NOT NULL DEFAULT 'ozon' CHECK(ozon_source='ozon'),
 FOREIGN KEY(tenant_id,moysklad_source,moysklad_account) REFERENCES stock_connections(tenant_id,source,account_id),
 FOREIGN KEY(tenant_id,ozon_source,ozon_account) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE TABLE stock_publications (
 id text PRIMARY KEY CHECK(length(id)=32),
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 review_id text NOT NULL REFERENCES stock_sync_reviews(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES stock_sync_settings(tenant_id,moysklad_account,ozon_account)
);
CREATE INDEX stock_publications_scope_idx ON stock_publications(tenant_id,moysklad_account,ozon_account,created_at DESC);
CREATE TABLE stock_publication_targets (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 operation_id text NOT NULL REFERENCES stock_publications(id),
 ozon_account text NOT NULL,
 product_id text NOT NULL,
 warehouse_id text NOT NULL,
 quantity text NOT NULL CHECK(quantity ~ '^[0-9]+$'),
 state text NOT NULL CHECK(state IN ('QUEUED','DISPATCHING','SUCCEEDED','FAILED','UNKNOWN','CONFIRMED_EFFECTIVE','CANCELLED','BLOCKED')),
 safe_error text NOT NULL DEFAULT '',
 decision jsonb NOT NULL CHECK(jsonb_typeof(decision)='object'),
 dispatched_at timestamptz,
 finished_at timestamptz,
 UNIQUE(operation_id,product_id,warehouse_id)
);
-- Pending and uncertain writes fence the physical target across every scope.
CREATE UNIQUE INDEX stock_publication_target_fence ON stock_publication_targets(ozon_account,product_id,warehouse_id) WHERE state IN ('QUEUED','DISPATCHING','UNKNOWN');
CREATE INDEX stock_publication_queue_idx ON stock_publication_targets(id) WHERE state='QUEUED';
CREATE TABLE stock_sync_mutations (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 idempotency_key text NOT NULL,
 request_hash text NOT NULL,
 action text NOT NULL CHECK(action IN ('enable','disable')),
 operation_id text REFERENCES stock_publications(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,idempotency_key)
);
CREATE TABLE stock_publication_dispatch_clock (
 ozon_account text PRIMARY KEY,
 dispatched_at timestamptz NOT NULL
);
CREATE INDEX stock_publication_target_dispatch_idx ON stock_publication_targets(ozon_account,product_id,warehouse_id,dispatched_at DESC) WHERE dispatched_at IS NOT NULL;
CREATE TRIGGER stock_sync_reviews_immutable BEFORE UPDATE OR DELETE ON stock_sync_reviews FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER stock_publications_immutable BEFORE UPDATE OR DELETE ON stock_publications FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER stock_sync_mutations_immutable BEFORE UPDATE OR DELETE ON stock_sync_mutations FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose StatementBegin
CREATE FUNCTION stock_publication_target_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'publication history is immutable'; END IF;
 IF (NEW.operation_id,NEW.ozon_account,NEW.product_id,NEW.warehouse_id,NEW.quantity,NEW.decision) IS DISTINCT FROM
    (OLD.operation_id,OLD.ozon_account,OLD.product_id,OLD.warehouse_id,OLD.quantity,OLD.decision) THEN
  RAISE EXCEPTION 'publication target and evidence are immutable';
 END IF;
 IF OLD.dispatched_at IS NOT NULL AND NEW.dispatched_at IS DISTINCT FROM OLD.dispatched_at THEN RAISE EXCEPTION 'dispatch fence is immutable'; END IF;
 IF NEW.state<>OLD.state AND NOT (
  (OLD.state='QUEUED' AND NEW.state IN ('DISPATCHING','CANCELLED','BLOCKED')) OR
  (OLD.state='DISPATCHING' AND NEW.state IN ('SUCCEEDED','FAILED','UNKNOWN','BLOCKED','CANCELLED')) OR
  (OLD.state='UNKNOWN' AND NEW.state='CONFIRMED_EFFECTIVE')
 ) THEN RAISE EXCEPTION 'invalid publication state transition'; END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd
CREATE TRIGGER stock_publication_target_immutable BEFORE UPDATE OR DELETE ON stock_publication_targets FOR EACH ROW EXECUTE FUNCTION stock_publication_target_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_publications) OR EXISTS(SELECT 1 FROM stock_sync_reviews) THEN
  RAISE EXCEPTION 'refusing to discard stock publication evidence';
 END IF;
END; $$;
-- +goose StatementEnd
DROP TABLE stock_publication_dispatch_clock;
DROP TABLE stock_sync_mutations;
DROP TABLE stock_publication_targets;
DROP FUNCTION stock_publication_target_guard();
DROP TABLE stock_publications;
DROP TABLE stock_sync_settings;
DROP TABLE stock_sync_reviews;
