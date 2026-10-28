-- +goose Up
CREATE TABLE source_imports (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 source text NOT NULL,
 account_id text NOT NULL,
 fingerprint text NOT NULL,
 started_at timestamptz NOT NULL,
 finished_at timestamptz NOT NULL,
 report_scope text NOT NULL
);
CREATE INDEX source_imports_latest_idx ON source_imports(source, account_id, id DESC);
CREATE TABLE source_entities (
 import_id bigint NOT NULL REFERENCES source_imports(id) ON DELETE CASCADE,
 kind text NOT NULL,
 external_id text NOT NULL,
 name text NOT NULL,
 code text NOT NULL,
 article text NOT NULL,
 parent_id text NOT NULL,
 source_updated text NOT NULL,
 PRIMARY KEY(import_id, kind, external_id)
);
CREATE TABLE source_stock_observations (
 import_id bigint NOT NULL REFERENCES source_imports(id) ON DELETE CASCADE,
 kind text NOT NULL,
 external_id text NOT NULL,
 source_href text NOT NULL,
 stock numeric,
 reserve numeric,
 in_transit numeric,
 quantity numeric,
 free_stock numeric,
 PRIMARY KEY(import_id, kind, external_id)
);
-- +goose Down
DROP TABLE source_stock_observations;
DROP TABLE source_entities;
DROP TABLE source_imports;
