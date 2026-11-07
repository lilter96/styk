-- +goose Up
CREATE TABLE product_import_drafts (
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 provider text NOT NULL CHECK(provider IN ('moysklad','ozon')),
 revision bigint NOT NULL CHECK(revision>0), state jsonb NOT NULL CHECK(jsonb_typeof(state)='object' AND octet_length(state::text)<=8388608),
 save_key text NOT NULL, save_hash text NOT NULL, updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,provider),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_configs(tenant_id,moysklad_account,ozon_account)
);
CREATE TABLE product_matching_jobs (
 id text PRIMARY KEY, tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 actor text NOT NULL, request_key text NOT NULL, revision bigint NOT NULL,
 moy_import bigint NOT NULL REFERENCES source_imports(id), ozon_import bigint NOT NULL REFERENCES source_imports(id),
 state text NOT NULL CHECK(state IN ('QUEUED','RUNNING','COMPLETED','STALE','FAILED')),
 cursor text NOT NULL DEFAULT '', processed integer NOT NULL DEFAULT 0, total integer NOT NULL DEFAULT 0,
 pages integer NOT NULL DEFAULT 0, summary jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(summary)='object'), code text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,moysklad_account,ozon_account,actor,request_key),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_configs(tenant_id,moysklad_account,ozon_account)
);
CREATE INDEX product_matching_jobs_queue ON product_matching_jobs(updated_at,id) WHERE state IN ('QUEUED','RUNNING');
CREATE TABLE product_matching_job_pages (
 job_id text NOT NULL REFERENCES product_matching_jobs(id), page integer NOT NULL,
 preview_id text NOT NULL REFERENCES product_matching_previews(id), PRIMARY KEY(job_id,page)
);
-- Page-sized exact matching uses a complete indexed catalogue, never a truncated snapshot.
-- +goose StatementBegin
CREATE FUNCTION product_matching_key(text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT btrim($1,E'\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000')
$$;
-- +goose StatementEnd
CREATE INDEX source_entities_match_article ON source_entities(import_id,product_matching_key(article),kind,external_id);
CREATE INDEX source_entities_match_code ON source_entities(import_id,product_matching_key(code),kind,external_id);
CREATE INDEX ozon_product_match_offer ON ozon_product_observations(import_id,product_matching_key(offer_id),product_id);
-- +goose StatementBegin
CREATE FUNCTION product_matching_words(text) RETURNS text[] LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT ARRAY(SELECT DISTINCT word FROM unnest(regexp_split_to_array(lower(translate($1,'Ёё','Ее')),'[^[:alnum:]]+')) word WHERE word<>'' ORDER BY word)
$$;
-- +goose StatementEnd
CREATE INDEX source_entities_match_name ON source_entities(import_id,lower(name));
CREATE INDEX source_entities_match_words ON source_entities USING gin(product_matching_words(name));
ALTER TABLE product_matching_policies ADD COLUMN batch_cursor text NOT NULL DEFAULT '';
ALTER TABLE product_matching_policies ADD COLUMN batch_moy_import bigint NOT NULL DEFAULT 0;
ALTER TABLE product_matching_policies ADD COLUMN batch_ozon_import bigint NOT NULL DEFAULT 0;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_import_drafts) OR EXISTS(SELECT 1 FROM product_matching_jobs) THEN
  RAISE EXCEPTION 'Product workspace data exists; retain migration 77 and use a forward fix';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE product_matching_policies DROP COLUMN batch_cursor, DROP COLUMN batch_moy_import, DROP COLUMN batch_ozon_import;
DROP INDEX ozon_product_match_offer;
DROP INDEX source_entities_match_code;
DROP INDEX source_entities_match_article;
DROP FUNCTION product_matching_key(text);
DROP INDEX source_entities_match_words;
DROP INDEX source_entities_match_name;
DROP FUNCTION product_matching_words(text);
DROP TABLE product_matching_job_pages;
DROP TABLE product_matching_jobs;
DROP TABLE product_import_drafts;
