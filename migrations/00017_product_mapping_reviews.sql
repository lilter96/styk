-- +goose Up
CREATE TABLE product_mapping_reviews (
 id text PRIMARY KEY CHECK (length(id) = 32),
 tenant_id text NOT NULL,
 ozon_source text NOT NULL DEFAULT 'ozon' CHECK (ozon_source='ozon'),
 ozon_account text NOT NULL,
 moysklad_source text NOT NULL DEFAULT 'moysklad' CHECK (moysklad_source='moysklad'),
 moysklad_account text NOT NULL,
 ozon_product_id text NOT NULL CHECK(length(trim(ozon_product_id)) > 0),
 target_kind text NOT NULL CHECK(target_kind IN ('product','variant')),
 target_id text NOT NULL CHECK(length(trim(target_id)) > 0),
 ozon_import_id bigint NOT NULL,
 moysklad_import_id bigint NOT NULL,
 existing_mapping_id bigint,
 existing_mapping_version integer CHECK(existing_mapping_version > 0),
 observation_hash text NOT NULL CHECK(length(observation_hash)=64),
 mapping_hash text NOT NULL CHECK(length(mapping_hash)=64),
 CHECK ((existing_mapping_id IS NULL) = (existing_mapping_version IS NULL)),
 evidence_class text NOT NULL CHECK(evidence_class IN ('DETERMINISTIC_CANDIDATE','OPERATOR_ATTESTED')),
 principal_type text NOT NULL CHECK(principal_type='LOCAL_OPERATOR_SESSION'),
 session_audit_id text NOT NULL CHECK(length(session_audit_id)>0),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>created_at AND expires_at<=created_at+interval '10 minutes'),
 FOREIGN KEY(tenant_id,ozon_source,ozon_account) REFERENCES stock_connections(tenant_id,source,account_id),
 FOREIGN KEY(tenant_id,moysklad_source,moysklad_account) REFERENCES stock_connections(tenant_id,source,account_id),
 FOREIGN KEY(ozon_import_id,ozon_source,ozon_account) REFERENCES source_imports(id,source,account_id),
 FOREIGN KEY(moysklad_import_id,moysklad_source,moysklad_account) REFERENCES source_imports(id,source,account_id),
 FOREIGN KEY(ozon_import_id,ozon_product_id) REFERENCES ozon_product_observations(import_id,product_id),
 FOREIGN KEY(moysklad_import_id,target_kind,target_id) REFERENCES source_entities(import_id,kind,external_id)
);
CREATE INDEX product_mapping_reviews_session_idx ON product_mapping_reviews (tenant_id,session_audit_id,created_at DESC);
CREATE TRIGGER product_mapping_reviews_immutable BEFORE UPDATE OR DELETE ON product_mapping_reviews FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM product_mapping_reviews) THEN RAISE EXCEPTION 'review history exists; refusing destructive rollback'; END IF; END $$;
-- +goose StatementEnd
DROP TABLE product_mapping_reviews;
