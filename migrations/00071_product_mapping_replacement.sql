-- +goose Up
-- A replacement appends a new version; old history, grants and outcomes remain
-- tied to their original version. Audit and idempotency must exist at commit.
CREATE TABLE product_mapping_replacements (
 mapping_id bigint NOT NULL,
 previous_version integer NOT NULL CHECK(previous_version>0),
 mapping_version integer NOT NULL CHECK(mapping_version=previous_version+1),
 review_id text NOT NULL UNIQUE,
 session_audit_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(mapping_id,mapping_version),
 FOREIGN KEY(mapping_id,previous_version) REFERENCES product_mapping_history(mapping_id,version),
 FOREIGN KEY(mapping_id,mapping_version,review_id,session_audit_id)
  REFERENCES product_mapping_confirmation_audits(mapping_id,mapping_version,review_id,session_audit_id)
  DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER product_mapping_replacements_immutable BEFORE UPDATE OR DELETE ON product_mapping_replacements FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_review_confirmed_mapping() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM product_mapping_confirmation_audits a WHERE a.mapping_id=OLD.id) THEN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'review-confirmed mapping cannot be revoked' USING ERRCODE='55000'; END IF;
  IF (NEW.id,NEW.tenant_id,NEW.ozon_account,NEW.moysklad_account,NEW.ozon_product_id)
   IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.ozon_account,OLD.moysklad_account,OLD.ozon_product_id)
   OR NEW.version<>OLD.version+1 OR NOT EXISTS (
    SELECT 1 FROM product_mapping_replacements x JOIN product_mapping_reviews r ON r.id=x.review_id
    WHERE x.mapping_id=OLD.id AND x.previous_version=OLD.version AND x.mapping_version=NEW.version
     AND r.existing_mapping_id=OLD.id AND r.existing_mapping_version=OLD.version
     AND r.session_audit_id=x.session_audit_id AND r.tenant_id=NEW.tenant_id
     AND r.ozon_account=NEW.ozon_account AND r.moysklad_account=NEW.moysklad_account
     AND r.ozon_product_id=NEW.ozon_product_id AND r.target_kind=NEW.target_kind AND r.target_id=NEW.target_id
     AND r.expires_at>clock_timestamp()
   ) THEN RAISE EXCEPTION 'replacement requires a current reviewed version and audit' USING ERRCODE='55000'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM product_mapping_replacements) THEN RAISE EXCEPTION 'Mapping replacement history exists; use a forward fix'; END IF; END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_review_confirmed_mapping() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM product_mapping_confirmation_audits a WHERE a.mapping_id=OLD.id) THEN
  RAISE EXCEPTION 'review-confirmed mapping cannot be replaced or revoked' USING ERRCODE='55000';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TABLE product_mapping_replacements;
