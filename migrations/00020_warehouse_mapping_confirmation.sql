-- +goose Up
-- Review-bound audit is additive; legacy warehouse mappings and history are not rewritten.
ALTER TABLE warehouse_mapping_reviews ADD CONSTRAINT warehouse_review_confirmation_scope UNIQUE
 (id,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id,ozon_import_id,moysklad_import_id,session_audit_id);
ALTER TABLE warehouse_mapping_history ADD COLUMN confirmation_review_id text REFERENCES warehouse_mapping_reviews(id);
ALTER TABLE warehouse_mapping_history ADD CONSTRAINT warehouse_history_review_scope UNIQUE
 (mapping_id,version,confirmation_review_id);

CREATE TABLE warehouse_mapping_confirmation_audits (
 mapping_id bigint NOT NULL,
 mapping_version integer NOT NULL CHECK(mapping_version>0),
 review_id text NOT NULL UNIQUE,
 tenant_id text NOT NULL,
 ozon_account text NOT NULL,
 moysklad_account text NOT NULL,
 moysklad_warehouse_id text NOT NULL,
 ozon_warehouse_id text NOT NULL,
 ozon_import_id bigint NOT NULL,
 moysklad_import_id bigint NOT NULL,
 evidence_class text NOT NULL CHECK(evidence_class='OPERATOR_ATTESTED'),
 principal_type text NOT NULL CHECK(principal_type='LOCAL_OPERATOR_SESSION'),
 session_audit_id text NOT NULL,
 explanation text NOT NULL CHECK(octet_length(explanation)<=1000),
 confirmed_at timestamptz NOT NULL,
 PRIMARY KEY(mapping_id,mapping_version),
 UNIQUE(mapping_id,mapping_version,review_id,session_audit_id),
 FOREIGN KEY(mapping_id,mapping_version,review_id)
  REFERENCES warehouse_mapping_history(mapping_id,version,confirmation_review_id),
 FOREIGN KEY(mapping_id,mapping_version,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id)
  REFERENCES warehouse_mapping_history(mapping_id,version,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id),
 FOREIGN KEY(review_id,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id,ozon_import_id,moysklad_import_id,session_audit_id)
  REFERENCES warehouse_mapping_reviews(id,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id,ozon_import_id,moysklad_import_id,session_audit_id)
);
CREATE TABLE warehouse_mapping_confirmation_idempotency (
 review_id text NOT NULL,
 session_audit_id text NOT NULL,
 idempotency_key text NOT NULL CHECK(idempotency_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 mapping_id bigint NOT NULL,
 mapping_version integer NOT NULL,
 PRIMARY KEY(review_id,session_audit_id,idempotency_key),
 FOREIGN KEY(mapping_id,mapping_version,review_id,session_audit_id)
  REFERENCES warehouse_mapping_confirmation_audits(mapping_id,mapping_version,review_id,session_audit_id)
);
CREATE TRIGGER warehouse_confirmation_audits_immutable BEFORE UPDATE OR DELETE ON warehouse_mapping_confirmation_audits FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER warehouse_confirmation_idempotency_immutable BEFORE UPDATE OR DELETE ON warehouse_mapping_confirmation_idempotency FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose StatementBegin
CREATE FUNCTION protect_review_bound_warehouse_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.confirmation_review_id IS NOT NULL THEN
  RAISE EXCEPTION 'review-bound warehouse history is immutable' USING ERRCODE='55000';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER warehouse_review_history_immutable BEFORE UPDATE OR DELETE ON warehouse_mapping_history FOR EACH ROW EXECUTE FUNCTION protect_review_bound_warehouse_history();

-- +goose StatementBegin
CREATE FUNCTION protect_review_confirmed_warehouse_mapping() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM warehouse_mapping_confirmation_audits a WHERE a.mapping_id=OLD.id) THEN
  RAISE EXCEPTION 'review-confirmed warehouse mapping cannot be replaced or revoked' USING ERRCODE='55000';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER warehouse_review_mapping_immutable BEFORE UPDATE OR DELETE ON warehouse_mappings FOR EACH ROW EXECUTE FUNCTION protect_review_confirmed_warehouse_mapping();

-- +goose StatementBegin
CREATE FUNCTION require_warehouse_confirmation_records() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.confirmation_review_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM warehouse_mapping_reviews r WHERE r.id=NEW.confirmation_review_id AND r.expires_at<=clock_timestamp()
 ) THEN
  RAISE EXCEPTION 'warehouse review expired before confirmation commit'
   USING ERRCODE='23514', CONSTRAINT='warehouse_review_unexpired';
 END IF;
 IF NEW.confirmation_review_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM warehouse_mapping_confirmation_audits a
  JOIN warehouse_mapping_confirmation_idempotency i
   ON i.mapping_id=a.mapping_id AND i.mapping_version=a.mapping_version
      AND i.review_id=a.review_id AND i.session_audit_id=a.session_audit_id
  JOIN warehouse_mappings m ON m.id=NEW.mapping_id AND m.version=NEW.version
   AND m.tenant_id=NEW.tenant_id AND m.ozon_account=NEW.ozon_account
   AND m.moysklad_account=NEW.moysklad_account AND m.moysklad_warehouse_id=NEW.moysklad_warehouse_id
   AND m.ozon_warehouse_id=NEW.ozon_warehouse_id AND m.status=NEW.status
   AND m.provenance=NEW.provenance AND m.confirmed_at=NEW.confirmed_at
  WHERE a.mapping_id=NEW.mapping_id AND a.mapping_version=NEW.version
   AND a.review_id=NEW.confirmation_review_id AND a.evidence_class='OPERATOR_ATTESTED'
   AND a.principal_type=NEW.provenance AND a.confirmed_at=NEW.confirmed_at
   AND a.ozon_import_id=NEW.ozon_import_id AND a.moysklad_import_id=NEW.moysklad_import_id
 ) THEN
  RAISE EXCEPTION 'review-bound warehouse mapping requires audit and idempotency outcome' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER warehouse_review_history_requires_confirmation
 AFTER INSERT OR UPDATE ON warehouse_mapping_history DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION require_warehouse_confirmation_records();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM warehouse_mapping_confirmation_audits) OR
    EXISTS(SELECT 1 FROM warehouse_mapping_confirmation_idempotency) OR
    EXISTS(SELECT 1 FROM warehouse_mapping_history WHERE confirmation_review_id IS NOT NULL) THEN
  RAISE EXCEPTION 'warehouse confirmation evidence exists; refusing destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER warehouse_review_history_requires_confirmation ON warehouse_mapping_history;
DROP FUNCTION require_warehouse_confirmation_records();
DROP TRIGGER warehouse_review_mapping_immutable ON warehouse_mappings;
DROP FUNCTION protect_review_confirmed_warehouse_mapping();
DROP TRIGGER warehouse_review_history_immutable ON warehouse_mapping_history;
DROP FUNCTION protect_review_bound_warehouse_history();
DROP TABLE warehouse_mapping_confirmation_idempotency;
DROP TABLE warehouse_mapping_confirmation_audits;
ALTER TABLE warehouse_mapping_history DROP CONSTRAINT warehouse_history_review_scope;
ALTER TABLE warehouse_mapping_history DROP COLUMN confirmation_review_id;
ALTER TABLE warehouse_mapping_reviews DROP CONSTRAINT warehouse_review_confirmation_scope;
