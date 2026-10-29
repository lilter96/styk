-- +goose Up
-- Existing administrative history remains unchanged. Only review-bound records
-- participate in the new audit and idempotency invariants.
-- Older reviews keep NULL fingerprint snapshots and require a fresh review;
-- immutable historical rows are not backfilled or silently trusted.
ALTER TABLE product_mapping_reviews
 ADD COLUMN ozon_import_fingerprint text,
 ADD COLUMN moysklad_import_fingerprint text,
 ADD CONSTRAINT product_review_fingerprints_pair CHECK
  ((ozon_import_fingerprint IS NULL) = (moysklad_import_fingerprint IS NULL));
ALTER TABLE product_mapping_reviews ADD CONSTRAINT product_review_confirmation_scope UNIQUE
 (id,tenant_id,ozon_account,moysklad_account,ozon_product_id,target_kind,target_id,ozon_import_id,moysklad_import_id,evidence_class,session_audit_id);
ALTER TABLE product_mapping_history ADD COLUMN confirmation_review_id text REFERENCES product_mapping_reviews(id);
ALTER TABLE product_mapping_history ADD CONSTRAINT product_history_review_scope UNIQUE
 (mapping_id,version,confirmation_review_id);

CREATE TABLE product_mapping_confirmation_audits (
 mapping_id bigint NOT NULL,
 mapping_version integer NOT NULL CHECK(mapping_version>0),
 review_id text NOT NULL UNIQUE,
 tenant_id text NOT NULL,
 ozon_account text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_product_id text NOT NULL,
 ozon_offer_id text NOT NULL,
 target_kind text NOT NULL CHECK(target_kind IN ('product','variant')),
 target_id text NOT NULL,
 ozon_import_id bigint NOT NULL,
 moysklad_import_id bigint NOT NULL,
 evidence_class text NOT NULL CHECK(evidence_class IN ('DETERMINISTIC_CANDIDATE','OPERATOR_ATTESTED')),
 principal_type text NOT NULL CHECK(principal_type='LOCAL_OPERATOR_SESSION'),
 session_audit_id text NOT NULL,
 explanation text NOT NULL CHECK(octet_length(explanation)<=1000),
 confirmed_at timestamptz NOT NULL,
 PRIMARY KEY(mapping_id,mapping_version),
 UNIQUE(mapping_id,mapping_version,review_id,session_audit_id),
 FOREIGN KEY(mapping_id,mapping_version,review_id)
   REFERENCES product_mapping_history(mapping_id,version,confirmation_review_id),
 FOREIGN KEY(mapping_id,mapping_version,tenant_id,ozon_account,moysklad_account,target_kind,target_id,ozon_product_id,ozon_offer_id)
   REFERENCES product_mapping_history(mapping_id,version,tenant_id,ozon_account,moysklad_account,target_kind,target_id,ozon_product_id,ozon_offer_id),
 FOREIGN KEY(review_id,tenant_id,ozon_account,moysklad_account,ozon_product_id,target_kind,target_id,ozon_import_id,moysklad_import_id,evidence_class,session_audit_id)
   REFERENCES product_mapping_reviews(id,tenant_id,ozon_account,moysklad_account,ozon_product_id,target_kind,target_id,ozon_import_id,moysklad_import_id,evidence_class,session_audit_id)
);

CREATE TABLE product_mapping_confirmation_idempotency (
 review_id text NOT NULL,
 session_audit_id text NOT NULL,
 idempotency_key text NOT NULL CHECK(idempotency_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 mapping_id bigint NOT NULL,
 mapping_version integer NOT NULL,
 PRIMARY KEY(review_id,session_audit_id,idempotency_key),
 FOREIGN KEY(mapping_id,mapping_version,review_id,session_audit_id)
   REFERENCES product_mapping_confirmation_audits(mapping_id,mapping_version,review_id,session_audit_id)
);
CREATE TRIGGER product_confirmation_audits_immutable BEFORE UPDATE OR DELETE ON product_mapping_confirmation_audits FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER product_confirmation_idempotency_immutable BEFORE UPDATE OR DELETE ON product_mapping_confirmation_idempotency FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose StatementBegin
CREATE FUNCTION protect_review_bound_mapping_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.confirmation_review_id IS NOT NULL THEN
  RAISE EXCEPTION 'review-bound mapping history is immutable' USING ERRCODE='55000';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER product_review_history_immutable BEFORE UPDATE OR DELETE ON product_mapping_history FOR EACH ROW EXECUTE FUNCTION protect_review_bound_mapping_history();

-- +goose StatementBegin
CREATE FUNCTION protect_review_confirmed_mapping() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM product_mapping_confirmation_audits a WHERE a.mapping_id=OLD.id) THEN
  RAISE EXCEPTION 'review-confirmed mapping cannot be replaced or revoked' USING ERRCODE='55000';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER product_review_mapping_immutable BEFORE UPDATE OR DELETE ON product_mappings FOR EACH ROW EXECUTE FUNCTION protect_review_confirmed_mapping();

-- +goose StatementBegin
CREATE FUNCTION require_product_confirmation_records() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.confirmation_review_id IS NOT NULL AND EXISTS (
   SELECT 1 FROM product_mapping_reviews r
   WHERE r.id=NEW.confirmation_review_id AND r.expires_at<=clock_timestamp()
 ) THEN
  RAISE EXCEPTION 'review expired before confirmation commit'
   USING ERRCODE='23514', CONSTRAINT='product_review_unexpired';
 END IF;
 IF NEW.confirmation_review_id IS NOT NULL AND NOT EXISTS (
   SELECT 1 FROM product_mapping_confirmation_audits a
   JOIN product_mapping_confirmation_idempotency i
     ON i.mapping_id=a.mapping_id AND i.mapping_version=a.mapping_version
        AND i.review_id=a.review_id AND i.session_audit_id=a.session_audit_id
   JOIN product_mappings m ON m.id=NEW.mapping_id AND m.version=NEW.version
     AND m.tenant_id=NEW.tenant_id AND m.ozon_account=NEW.ozon_account
     AND m.moysklad_account=NEW.moysklad_account AND m.ozon_product_id=NEW.ozon_product_id
     AND m.ozon_offer_id=NEW.ozon_offer_id AND m.target_kind=NEW.target_kind
     AND m.target_id=NEW.target_id AND m.provenance=NEW.provenance AND m.confirmed_at=NEW.confirmed_at
   WHERE a.mapping_id=NEW.mapping_id AND a.mapping_version=NEW.version
     AND a.review_id=NEW.confirmation_review_id
     AND a.principal_type=NEW.provenance AND a.confirmed_at=NEW.confirmed_at
     AND a.ozon_import_id=NEW.ozon_import_id AND a.moysklad_import_id=NEW.moysklad_import_id
 ) THEN
  RAISE EXCEPTION 'review-bound mapping requires audit and idempotency outcome' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER product_review_history_requires_confirmation
 AFTER INSERT OR UPDATE ON product_mapping_history DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION require_product_confirmation_records();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_mapping_confirmation_audits) OR
    EXISTS(SELECT 1 FROM product_mapping_confirmation_idempotency) OR
    EXISTS(SELECT 1 FROM product_mapping_reviews WHERE ozon_import_fingerprint IS NOT NULL OR moysklad_import_fingerprint IS NOT NULL) OR
    EXISTS(SELECT 1 FROM product_mapping_history WHERE confirmation_review_id IS NOT NULL) THEN
  RAISE EXCEPTION 'review or confirmation evidence exists; refusing destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER product_review_history_requires_confirmation ON product_mapping_history;
DROP FUNCTION require_product_confirmation_records();
DROP TRIGGER product_review_mapping_immutable ON product_mappings;
DROP FUNCTION protect_review_confirmed_mapping();
DROP TRIGGER product_review_history_immutable ON product_mapping_history;
DROP FUNCTION protect_review_bound_mapping_history();
DROP TABLE product_mapping_confirmation_idempotency;
DROP TABLE product_mapping_confirmation_audits;
ALTER TABLE product_mapping_history DROP CONSTRAINT product_history_review_scope;
ALTER TABLE product_mapping_history DROP COLUMN confirmation_review_id;
ALTER TABLE product_mapping_reviews DROP CONSTRAINT product_review_confirmation_scope;
ALTER TABLE product_mapping_reviews DROP CONSTRAINT product_review_fingerprints_pair;
ALTER TABLE product_mapping_reviews DROP COLUMN moysklad_import_fingerprint;
ALTER TABLE product_mapping_reviews DROP COLUMN ozon_import_fingerprint;
