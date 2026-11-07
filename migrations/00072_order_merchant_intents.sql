-- +goose Up
CREATE TABLE orders_merchant_intents (
    id uuid PRIMARY KEY,
    tenant_id text NOT NULL,
    moysklad_account text NOT NULL,
    ozon_account text NOT NULL,
    posting_number text NOT NULL,
    customer_order_id text NOT NULL,
    config_revision bigint NOT NULL,
    digest text NOT NULL,
    intent jsonb NOT NULL,
    state text NOT NULL CHECK (state IN ('OBSERVED','BLOCKED','QUEUED')),
    code text NOT NULL DEFAULT '' CHECK (length(code)<=128 AND code ~ '^[a-zA-Z0-9_]*$'),
    work_id text UNIQUE,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (digest ~ '^[0-9a-f]{64}$'),
    CHECK ((state='QUEUED')=(work_id IS NOT NULL)),
    CHECK ((intent->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account)
      AND intent->>'id'=id::text AND intent->>'digest'=digest
      AND intent#>>'{correlation,posting_number}'=posting_number
      AND intent#>>'{correlation,customer_order_id}'=customer_order_id
      AND (intent->>'config_revision')::bigint=config_revision) IS TRUE),
    UNIQUE (tenant_id,moysklad_account,ozon_account,digest),
    FOREIGN KEY (tenant_id,moysklad_account,ozon_account,posting_number)
      REFERENCES orders_projection_bindings(tenant_id,moysklad_account,ozon_account,posting_number),
    FOREIGN KEY (work_id,tenant_id,moysklad_account,ozon_account)
      REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account),
    FOREIGN KEY (tenant_id,moysklad_account,ozon_account,config_revision)
      REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision)
);
CREATE INDEX orders_merchant_intents_pending ON orders_merchant_intents
 (tenant_id,moysklad_account,ozon_account,recorded_at,id) WHERE state='OBSERVED';
CREATE UNIQUE INDEX orders_merchant_intents_source_action ON orders_merchant_intents
 (tenant_id,moysklad_account,ozon_account,customer_order_id,(intent->>'after_revision'),(intent->>'action'));
-- +goose StatementBegin
CREATE FUNCTION orders_merchant_intent_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.id,NEW.tenant_id,NEW.moysklad_account,NEW.ozon_account,NEW.posting_number,
      NEW.customer_order_id,NEW.config_revision,NEW.digest,NEW.intent,NEW.recorded_at)
      IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.moysklad_account,OLD.ozon_account,OLD.posting_number,
      OLD.customer_order_id,OLD.config_revision,OLD.digest,OLD.intent,OLD.recorded_at)
      OR (OLD.state <> 'OBSERVED' AND ROW(NEW.state,NEW.code,NEW.work_id) IS DISTINCT FROM ROW(OLD.state,OLD.code,OLD.work_id)) THEN
      RAISE EXCEPTION 'merchant intent is immutable after admission';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_merchant_intent_immutable BEFORE UPDATE ON orders_merchant_intents
 FOR EACH ROW EXECUTE FUNCTION orders_merchant_intent_immutable();
CREATE TRIGGER orders_merchant_intent_no_delete BEFORE DELETE ON orders_merchant_intents
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- Preserve audit and policy authority. Operators must export history and revoke
-- lifecycle permissions before considering an older binary/schema.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM orders_merchant_intents) OR EXISTS (
   SELECT 1 FROM orders_config_revisions WHERE config->'lifecycle' IS NOT NULL AND config->'lifecycle' <> 'null'::jsonb
 ) THEN RAISE EXCEPTION 'cannot discard merchant intent or lifecycle policy history'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE orders_merchant_intents;
DROP FUNCTION orders_merchant_intent_immutable();
