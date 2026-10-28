-- +goose Up
CREATE TABLE warehouse_mappings (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id text NOT NULL CHECK (length(trim(tenant_id))>0),
 moysklad_account text NOT NULL CHECK (length(trim(moysklad_account))>0),
 ozon_account text NOT NULL CHECK (length(trim(ozon_account))>0),
 moysklad_warehouse_id text NOT NULL CHECK (length(trim(moysklad_warehouse_id))>0),
 ozon_warehouse_id text NOT NULL CHECK (length(trim(ozon_warehouse_id))>0),
 status text NOT NULL CHECK (status='active'),
 version integer NOT NULL CHECK (version=1),
 provenance text NOT NULL CHECK (length(trim(provenance))>0),
 confirmed_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id),
 UNIQUE (tenant_id,moysklad_account,ozon_account,ozon_warehouse_id)
);
CREATE TABLE warehouse_mapping_history (
 mapping_id bigint NOT NULL REFERENCES warehouse_mappings(id),
 version integer NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 moysklad_warehouse_id text NOT NULL,
 ozon_warehouse_id text NOT NULL,
 status text NOT NULL,
 provenance text NOT NULL,
 confirmed_at timestamptz NOT NULL,
 moysklad_import_id bigint NOT NULL REFERENCES source_imports(id),
 ozon_import_id bigint NOT NULL REFERENCES source_imports(id),
 PRIMARY KEY(mapping_id,version)
);
-- +goose Down
DROP TABLE warehouse_mapping_history;
DROP TABLE warehouse_mappings;
