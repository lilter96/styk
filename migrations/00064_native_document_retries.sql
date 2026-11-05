-- +goose Up
CREATE TABLE orders_native_document_events (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 kind text NOT NULL CHECK(kind IN('demand','salesreturn')),
 event_digest text NOT NULL CHECK(event_digest ~ '^[0-9a-f]{64}$'),
 sync_id uuid NOT NULL,
 marker text NOT NULL CHECK(length(marker) BETWEEN 1 AND 255),
 ozon_account text NOT NULL,
 posting_number text NOT NULL,
 customer_order_id text NOT NULL,
 origin_work_id text NOT NULL UNIQUE,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,moysklad_account,kind,event_digest),
 UNIQUE(tenant_id,moysklad_account,kind,sync_id),
 UNIQUE(tenant_id,moysklad_account,kind,marker),
 UNIQUE(tenant_id,moysklad_account,ozon_account,kind,event_digest,sync_id,marker,posting_number,customer_order_id),
 FOREIGN KEY(origin_work_id,tenant_id,moysklad_account,ozon_account,kind,posting_number,customer_order_id)
  REFERENCES orders_native_document_intents(work_id,tenant_id,moysklad_account,ozon_account,kind,posting_number,customer_order_id)
  DEFERRABLE INITIALLY DEFERRED
);
INSERT INTO orders_native_document_events
 (tenant_id,moysklad_account,kind,event_digest,sync_id,marker,ozon_account,posting_number,customer_order_id,origin_work_id,created_at)
 SELECT tenant_id,moysklad_account,kind,event_digest,sync_id,marker,ozon_account,posting_number,customer_order_id,work_id,created_at
 FROM orders_native_document_intents;
CREATE TRIGGER orders_native_document_events_immutable BEFORE UPDATE OR DELETE ON orders_native_document_events
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- Replace only the three original canonical-event uniqueness constraints. Other
-- scope, work, result and return-basis constraints remain unchanged.
-- +goose StatementBegin
DO $$ DECLARE entry record; removed integer:=0; BEGIN
 FOR entry IN SELECT c.conname FROM pg_constraint c
 WHERE c.conrelid='orders_native_document_intents'::regclass AND c.contype='u'
 AND (SELECT array_agg(a.attname::text ORDER BY k.ordinality)
  FROM unnest(c.conkey) WITH ORDINALITY k(attnum,ordinality)
  JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum)
 IN (ARRAY['tenant_id','moysklad_account','kind','event_digest'],
     ARRAY['tenant_id','moysklad_account','kind','sync_id'],
     ARRAY['tenant_id','moysklad_account','kind','marker'])
 LOOP
  EXECUTE format('ALTER TABLE orders_native_document_intents DROP CONSTRAINT %I',entry.conname);
  removed:=removed+1;
 END LOOP;
 IF removed<>3 THEN RAISE EXCEPTION 'native event uniqueness baseline is incompatible'; END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX orders_native_intent_canonical_history ON orders_native_document_intents
 (tenant_id,moysklad_account,kind,event_digest,created_at,work_id);
ALTER TABLE orders_native_document_intents ADD CONSTRAINT orders_native_intent_event_key
 UNIQUE(work_id,tenant_id,moysklad_account,ozon_account,kind,event_digest);
ALTER TABLE orders_native_document_intents ADD CONSTRAINT orders_native_intent_canonical_event
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,kind,event_digest,sync_id,marker,posting_number,customer_order_id)
 REFERENCES orders_native_document_events(tenant_id,moysklad_account,ozon_account,kind,event_digest,sync_id,marker,posting_number,customer_order_id);

CREATE TABLE orders_native_document_retries (
 work_id text PRIMARY KEY,
 previous_work_id text NOT NULL UNIQUE CHECK(previous_work_id<>work_id),
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 kind text NOT NULL,
 event_digest text NOT NULL,
 operation_id text NOT NULL,
 owner_user_id text NOT NULL,
 actor text NOT NULL,
 created_at timestamptz NOT NULL,
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account,kind,event_digest)
 REFERENCES orders_native_document_intents(work_id,tenant_id,moysklad_account,ozon_account,kind,event_digest),
 FOREIGN KEY(previous_work_id,tenant_id,moysklad_account,ozon_account,kind,event_digest)
 REFERENCES orders_native_document_intents(work_id,tenant_id,moysklad_account,ozon_account,kind,event_digest),
 FOREIGN KEY(work_id,operation_id,tenant_id,moysklad_account,ozon_account)
 REFERENCES orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES saas_merchant_accounts(tenant_id,user_id)
);
CREATE TRIGGER orders_native_document_retries_immutable BEFORE UPDATE OR DELETE ON orders_native_document_retries
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- This denial-only predicate never promotes an UNKNOWN or 409 to a rejection.
-- +goose StatementBegin
CREATE FUNCTION orders_native_retry_no_effect(wid text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT COALESCE(bool_and(
 COALESCE(w.work->>'code','')<>'NATIVE_RETRY_EXISTING_DOCUMENT_REQUIRES_REVIEW'
 AND COALESCE(w.work#>>'{receipt,http_status}','0')<>'409'
 AND NOT EXISTS(SELECT 1 FROM orders_reconciliations r WHERE r.work_id=w.id)
 AND NOT EXISTS(SELECT 1 FROM orders_native_document_results n WHERE n.work_id=w.id)
 AND ((w.dispatch_count=0 AND w.state IN('REJECTED','BLOCKED')
  AND COALESCE(w.work#>>'{receipt,state}','') IN('','REJECTED','BLOCKED')
  AND NOT EXISTS(SELECT 1 FROM orders_write_intents d WHERE d.work_id=w.id))
 OR (w.dispatch_count=1 AND w.state='REJECTED'
  AND w.work#>>'{receipt,state}'='REJECTED'
  AND w.work#>>'{receipt,command_id}'=w.command_id
  AND COALESCE(w.work#>>'{receipt,external_id}','')=''
  AND w.work#>>'{receipt,code}'='PROVIDER_REJECTED'
  AND w.work#>>'{receipt,http_status}' IN('400','401','403','404','405','412','413','415','422','429')
  AND EXISTS(SELECT 1 FROM orders_write_intents d WHERE d.work_id=w.id AND d.tenant_id=w.tenant_id
   AND d.moysklad_account=w.moysklad_account AND d.ozon_account=w.ozon_account
   AND d.fingerprint=w.command_fingerprint AND d.command=w.work->'command')))),false)
 FROM orders_work w WHERE w.id=wid
$$;
-- +goose StatementEnd

-- Every non-origin intent requires an audited, non-forking predecessor. The
-- application additionally validates the complete typed history under this
-- canonical event's row lock before creating the intent.
-- +goose StatementBegin
CREATE FUNCTION orders_native_retry_chain_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE origin text; current_work orders_work%ROWTYPE; prior orders_work%ROWTYPE; link orders_native_document_retries%ROWTYPE; frozen jsonb;
BEGIN
 SELECT origin_work_id INTO origin FROM orders_native_document_events
 WHERE tenant_id=NEW.tenant_id AND moysklad_account=NEW.moysklad_account AND kind=NEW.kind AND event_digest=NEW.event_digest;
 SELECT * INTO current_work FROM orders_work WHERE id=NEW.work_id;
 frozen:=current_work.work#>'{command,native_movement,retry}';
 IF current_work.work#>>'{command,native_movement,event,digest}' IS DISTINCT FROM NEW.event_digest
 OR current_work.work#>>'{command,native_movement,correlation,sync_id}' IS DISTINCT FROM NEW.sync_id::text
 OR current_work.work#>>'{command,native_movement,correlation,marker}' IS DISTINCT FROM NEW.marker
 OR current_work.work#>>'{command,correlation,customer_order_id}' IS DISTINCT FROM NEW.customer_order_id THEN
  RAISE EXCEPTION 'native intent differs from its canonical event' USING ERRCODE='23514';
 END IF;
 IF origin=NEW.work_id THEN
  IF frozen IS NOT NULL AND frozen<>'null'::jsonb THEN
   RAISE EXCEPTION 'initial native event cannot claim a retry' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 SELECT * INTO link FROM orders_native_document_retries WHERE work_id=NEW.work_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'duplicate native event requires explicit retry authorization' USING ERRCODE='23514'; END IF;
 SELECT * INTO prior FROM orders_work WHERE id=link.previous_work_id;
 IF NOT (prior.state='REJECTED' OR (prior.state='BLOCKED' AND prior.dispatch_count=0
  AND NOT EXISTS(SELECT 1 FROM orders_write_intents d WHERE d.work_id=prior.id)))
 OR NOT orders_native_retry_no_effect(prior.id)
 OR frozen#>>'{previous_work_id}' IS DISTINCT FROM prior.id
 OR frozen#>>'{previous_command_fingerprint}' IS DISTINCT FROM prior.command_fingerprint
 OR frozen#>>'{event_digest}' IS DISTINCT FROM NEW.event_digest
 OR prior.created_at>current_work.created_at
 OR EXISTS(SELECT 1 FROM orders_native_document_intents i JOIN orders_work w ON w.id=i.work_id
  WHERE i.tenant_id=NEW.tenant_id AND i.moysklad_account=NEW.moysklad_account
  AND i.kind=NEW.kind AND i.event_digest=NEW.event_digest AND i.work_id<>NEW.work_id
  AND (NOT orders_native_retry_no_effect(w.id) OR (w.created_at,w.id)>(prior.created_at,prior.id))) THEN
  RAISE EXCEPTION 'native retry predecessor or no-effect history is unverified' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER orders_native_retry_chain_valid AFTER INSERT ON orders_native_document_intents
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION orders_native_retry_chain_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_native_document_retries) THEN
  RAISE EXCEPTION 'native retry authorization history prevents rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER orders_native_retry_chain_valid ON orders_native_document_intents;
DROP FUNCTION orders_native_retry_chain_guard();
DROP FUNCTION orders_native_retry_no_effect(text);
DROP TABLE orders_native_document_retries;
DROP INDEX orders_native_intent_canonical_history;
ALTER TABLE orders_native_document_intents DROP CONSTRAINT orders_native_intent_canonical_event;
ALTER TABLE orders_native_document_intents DROP CONSTRAINT orders_native_intent_event_key;
DROP TABLE orders_native_document_events;
ALTER TABLE orders_native_document_intents ADD CONSTRAINT orders_native_event_digest_unique UNIQUE(tenant_id,moysklad_account,kind,event_digest);
ALTER TABLE orders_native_document_intents ADD CONSTRAINT orders_native_event_sync_unique UNIQUE(tenant_id,moysklad_account,kind,sync_id);
ALTER TABLE orders_native_document_intents ADD CONSTRAINT orders_native_event_marker_unique UNIQUE(tenant_id,moysklad_account,kind,marker);
