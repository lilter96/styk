-- +goose Up
CREATE TABLE product_mappings (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id text NOT NULL CHECK (length(trim(tenant_id))>0),
 ozon_account text NOT NULL CHECK (length(trim(ozon_account))>0),
 ozon_product_id text NOT NULL CHECK (length(trim(ozon_product_id))>0),
 ozon_offer_id text NOT NULL CHECK (length(trim(ozon_offer_id))>0),
 moysklad_account text NOT NULL CHECK (length(trim(moysklad_account))>0),
 target_kind text NOT NULL CHECK (target_kind IN ('product','variant')),
 target_id text NOT NULL CHECK (length(trim(target_id))>0),
 version integer NOT NULL CHECK (version > 0),
 provenance text NOT NULL CHECK (length(trim(provenance))>0),
 confirmed_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (tenant_id, ozon_account, ozon_product_id),
 UNIQUE (tenant_id, ozon_account, ozon_offer_id)
);
CREATE INDEX product_mappings_target_idx ON product_mappings (tenant_id, moysklad_account, target_kind, target_id);
CREATE TABLE product_mapping_history (
 mapping_id bigint NOT NULL REFERENCES product_mappings(id),
 version integer NOT NULL CHECK(version>0),
 tenant_id text NOT NULL,
 ozon_account text NOT NULL,
 ozon_product_id text NOT NULL,
 ozon_offer_id text NOT NULL,
 moysklad_account text NOT NULL,
 target_kind text NOT NULL CHECK(target_kind IN ('product','variant')),
 target_id text NOT NULL,
 provenance text NOT NULL CHECK(length(trim(provenance))>0),
 confirmed_at timestamptz NOT NULL,
 ozon_import_id bigint NOT NULL REFERENCES source_imports(id),
 moysklad_import_id bigint NOT NULL REFERENCES source_imports(id),
 PRIMARY KEY(mapping_id,version)
);
-- +goose Down
DROP TABLE product_mapping_history;
DROP TABLE product_mappings;
