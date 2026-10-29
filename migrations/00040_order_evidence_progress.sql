-- +goose Up
-- Only normalized, read-only evidence is staged. Neither an unfinished window
-- nor its temporary observations are visible as a completed evidence batch.
CREATE TABLE order_evidence_progress (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_source text NOT NULL DEFAULT 'moysklad' CHECK(moysklad_source='moysklad'),
 moysklad_account text NOT NULL,
 ozon_source text NOT NULL DEFAULT 'ozon' CHECK(ozon_source='ozon'),
 ozon_account text NOT NULL,
 window_hours integer NOT NULL CHECK(window_hours IN (1,24,168)),
 window_from timestamptz NOT NULL,
 window_to timestamptz NOT NULL CHECK(window_to > window_from),
 started_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 moysklad_revision bigint NOT NULL CHECK(moysklad_revision >= 0),
 ozon_revision bigint NOT NULL CHECK(ozon_revision >= 0),
 generation bigint NOT NULL CHECK(generation > 0),
 version bigint NOT NULL CHECK(version > 0),
 source_status text NOT NULL CHECK(source_status IN ('IN_PROGRESS','COMPLETED','LIMIT_REACHED','RATE_LIMITED','AUTHORIZATION_REQUIRED','UNAVAILABLE','INVALID_RESPONSE','CONFLICT')),
 posting_status text NOT NULL CHECK(posting_status IN ('IN_PROGRESS','COMPLETED','LIMIT_REACHED','RATE_LIMITED','AUTHORIZATION_REQUIRED','UNAVAILABLE','INVALID_RESPONSE','CONFLICT')),
 source_resource text NOT NULL DEFAULT '' CHECK(source_resource IN ('','requests','attempts','pages','products','variants','warehouses','observations','warehouse_observations','entities','postings','response_bytes','duration','staging_bytes','other')),
 posting_resource text NOT NULL DEFAULT '' CHECK(posting_resource IN ('','requests','attempts','pages','products','variants','warehouses','observations','warehouse_observations','entities','postings','response_bytes','duration','staging_bytes','other')),
 source_completed_windows integer NOT NULL DEFAULT 0 CHECK(source_completed_windows >= 0),
 source_pending_windows integer NOT NULL DEFAULT 1 CHECK(source_pending_windows >= 0),
 posting_completed_windows integer NOT NULL DEFAULT 0 CHECK(posting_completed_windows >= 0),
 posting_pending_windows integer NOT NULL DEFAULT 1 CHECK(posting_pending_windows >= 0),
 source_batch_id bigint,
 posting_batch_id bigint,
 source_progress jsonb NOT NULL CHECK(jsonb_typeof(source_progress)='object' AND octet_length(source_progress::text) <= 8388608),
 posting_progress jsonb NOT NULL CHECK(jsonb_typeof(posting_progress)='object' AND octet_length(posting_progress::text) <= 8388608),
 CHECK((source_status='COMPLETED') = (source_batch_id IS NOT NULL)),
 CHECK((posting_status='COMPLETED') = (posting_batch_id IS NOT NULL)),
 FOREIGN KEY(tenant_id,moysklad_source,moysklad_account) REFERENCES stock_connections(tenant_id,source,account_id),
 FOREIGN KEY(tenant_id,ozon_source,ozon_account) REFERENCES stock_connections(tenant_id,source,account_id),
 FOREIGN KEY(source_batch_id,tenant_id,moysklad_account) REFERENCES moy_order_batches(id,tenant_id,account_id),
 FOREIGN KEY(posting_batch_id,tenant_id,ozon_account) REFERENCES ozon_posting_batches(id,tenant_id,account_id),
 UNIQUE(tenant_id,moysklad_account,ozon_account)
);

-- A result must belong to this exact frozen traversal, not merely the same
-- account. The batch and completion marker are inserted in one transaction.
-- +goose StatementBegin
CREATE FUNCTION order_evidence_result_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.source_batch_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM moy_order_batches b WHERE b.id=NEW.source_batch_id
   AND b.tenant_id=NEW.tenant_id AND b.account_id=NEW.moysklad_account
   AND b.window_from=NEW.window_from AND b.window_to=NEW.window_to
   AND b.started_at=NEW.started_at AND b.traversal_status='completed'
 ) THEN
  RAISE EXCEPTION 'source result does not match the frozen order window' USING ERRCODE='23514';
 END IF;
 IF NEW.posting_batch_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM ozon_posting_batches b WHERE b.id=NEW.posting_batch_id
   AND b.tenant_id=NEW.tenant_id AND b.account_id=NEW.ozon_account
   AND b.window_from=NEW.window_from AND b.window_to=NEW.window_to
   AND b.started_at=NEW.started_at AND b.traversal_status='completed'
 ) THEN
  RAISE EXCEPTION 'posting result does not match the frozen order window' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER order_evidence_result_guard BEFORE INSERT OR UPDATE ON order_evidence_progress
 FOR EACH ROW EXECUTE FUNCTION order_evidence_result_guard();

-- +goose Down
-- Do not silently discard incomplete progress or completed run diagnostics.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM order_evidence_progress) THEN
  RAISE EXCEPTION 'refusing to discard order-evidence progress history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE order_evidence_progress;
DROP FUNCTION order_evidence_result_guard();
