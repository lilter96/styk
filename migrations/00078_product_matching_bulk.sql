-- +goose Up
ALTER TABLE product_matching_jobs
 ADD COLUMN criterion text NOT NULL DEFAULT 'article_or_code' CHECK(length(criterion) BETWEEN 1 AND 64),
 ADD COLUMN apply_state text NOT NULL DEFAULT 'NOT_STARTED' CHECK(apply_state IN ('NOT_STARTED','QUEUED','RUNNING','COMPLETED','STALE','FAILED','CANCELLED')),
 ADD COLUMN apply_key text NOT NULL DEFAULT '',
 ADD COLUMN apply_total integer NOT NULL DEFAULT 0,
 ADD COLUMN confirmed integer NOT NULL DEFAULT 0,
 ADD COLUMN skipped integer NOT NULL DEFAULT 0,
 ADD COLUMN apply_code text NOT NULL DEFAULT '';
CREATE INDEX product_matching_apply_queue ON product_matching_jobs(updated_at,id) WHERE apply_state IN ('QUEUED','RUNNING');
ALTER TABLE product_matching_jobs ADD CONSTRAINT product_matching_jobs_scope UNIQUE(id,tenant_id,moysklad_account,ozon_account);
ALTER TABLE product_matching_cycles ALTER COLUMN run_id DROP NOT NULL, ADD COLUMN job_id text,
 ADD CONSTRAINT product_matching_cycle_job_scope FOREIGN KEY(job_id,tenant_id,moysklad_account,ozon_account) REFERENCES product_matching_jobs(id,tenant_id,moysklad_account,ozon_account),
 ADD CONSTRAINT product_matching_cycle_evidence CHECK(num_nonnulls(run_id,job_id)=1);
CREATE TABLE product_matching_job_candidates (
 job_id text NOT NULL REFERENCES product_matching_jobs(id), product_id text NOT NULL,
 status text NOT NULL CHECK(status IN ('exact','confirmed','conflict','review','unmatched')),
 target_kind text, target_id text, item jsonb NOT NULL CHECK(item->>'status'=status),
 PRIMARY KEY(job_id,product_id)
);
CREATE INDEX product_matching_job_candidates_status ON product_matching_job_candidates(job_id,status,product_id);
CREATE TRIGGER product_matching_candidates_immutable BEFORE UPDATE OR DELETE ON product_matching_job_candidates FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
-- Existing read-only scans remain audit evidence; the new workflow computes an entire set.
UPDATE product_matching_jobs SET state='STALE',code='MATCHING_METHOD_CHANGED' WHERE state IN ('QUEUED','RUNNING');
CREATE TABLE product_matching_job_results (
 job_id text PRIMARY KEY REFERENCES product_matching_jobs(id), actor text NOT NULL, request_key text NOT NULL,
 confirmed integer NOT NULL CHECK(confirmed>=0), skipped integer NOT NULL CHECK(skipped>=0), total integer NOT NULL CHECK(total=confirmed+skipped),
 finished_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER product_matching_results_immutable BEFORE UPDATE OR DELETE ON product_matching_job_results FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
-- Validate each distinct actor after the complete statement, with the same
-- lineage locks and clock checks as row writes. Invalid actors roll back all rows.
-- +goose StatementBegin
CREATE FUNCTION product_matching_validate_actors() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actor record;
BEGIN
 FOR actor IN SELECT DISTINCT tenant_id,session_audit_id FROM matching_actor_rows WHERE principal_type='MERCHANT_SESSION' LOOP
  PERFORM saas_require_actor(actor.tenant_id,actor.session_audit_id,false);
 END LOOP;
 RETURN NULL;
END $$;
-- +goose StatementEnd
DROP TRIGGER saas_actor_product_review ON product_mapping_reviews;
DROP TRIGGER saas_actor_product_confirmation ON product_mapping_confirmation_audits;
CREATE TRIGGER saas_actor_product_review AFTER INSERT ON product_mapping_reviews REFERENCING NEW TABLE AS matching_actor_rows FOR EACH STATEMENT EXECUTE FUNCTION product_matching_validate_actors();
CREATE TRIGGER saas_actor_product_confirmation AFTER INSERT ON product_mapping_confirmation_audits REFERENCING NEW TABLE AS matching_actor_rows FOR EACH STATEMENT EXECUTE FUNCTION product_matching_validate_actors();
-- Same canonical string encoding as encoding/json (including its HTML and separator escaping).
-- +goose StatementBegin
CREATE FUNCTION product_matching_escape(text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT COALESCE(replace(replace(replace(replace(replace($1,'&',E'\\u0026'),'<',E'\\u003c'),'>',E'\\u003e'),chr(8232),E'\\u2028'),chr(8233),E'\\u2029'),'null')
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION product_matching_json(text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT product_matching_escape(to_json($1)::text)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION product_matching_observation_hash(oi bigint,mi bigint,offer text,sku text,archived boolean,name text,article text,code text,parent text,updated text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT encode(sha256(convert_to(product_matching_escape(array_to_json(ARRAY[to_json(oi),to_json(mi),to_json(offer),to_json(sku),to_json(archived),to_json(name),to_json(article),to_json(code),to_json(parent),to_json(updated)])::text),'UTF8')),'hex')
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION product_matching_confirmation_hash(review_id text,explanation text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT encode(sha256(convert_to('{"version":1,"review_id":'||product_matching_json(review_id)||',"acknowledged":true,"explanation":'||product_matching_json(explanation)||'}','UTF8')),'hex')
$$;
-- +goose StatementEnd
-- A stable review identity makes every evidence stage a linear pass, without joining returned writes.
-- +goose StatementBegin
CREATE FUNCTION product_matching_review_id(job_id text,product_id text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT left(encode(sha256(convert_to(job_id||':'||product_id,'UTF8')),'hex'),32)
$$;
-- +goose StatementEnd
-- Criterion fields enter one common indexed set operation. There is no catalogue-size branch.
-- +goose StatementBegin
CREATE FUNCTION product_matching_rows(tenant text,moy text,ozon text,mi bigint,oi bigint,fields text[])
 RETURNS TABLE(product_id text,offer_id text,name text,status text,reason text,target_kind text,target_id text,candidates jsonb,shared_inventory boolean,sku text,archived boolean,target_name text,article text,code text,parent_id text,source_updated text)
 LANGUAGE sql STABLE PARALLEL SAFE AS $$
 WITH target_keys AS MATERIALIZED (
 SELECT kind,external_id,product_matching_key(article) AS key FROM source_entities WHERE import_id=mi AND kind IN ('product','variant') AND 'article'=ANY(fields) AND product_matching_key(article)<>''
 UNION SELECT kind,external_id,product_matching_key(code) AS key FROM source_entities WHERE import_id=mi AND kind IN ('product','variant') AND 'code'=ANY(fields) AND product_matching_key(code)<>''
 ), grouped AS MATERIALIZED(SELECT key,count(*) n,min(kind) kind,min(external_id) id FROM target_keys GROUP BY key),
 products AS(SELECT p.*,product_matching_key(p.offer_id) AS key,count(*) OVER(PARTITION BY product_matching_key(p.offer_id)) AS duplicates FROM ozon_product_observations p WHERE p.import_id=oi),
 relevant_targets AS MATERIALIZED(
 SELECT moy AS account,kind,external_id AS id FROM target_keys
 UNION SELECT m.moysklad_account,m.target_kind,m.target_id FROM product_mappings m JOIN products p ON p.product_id=m.ozon_product_id WHERE m.tenant_id=tenant AND m.ozon_account=ozon),
 target_usage AS MATERIALIZED(SELECT m.moysklad_account AS target_account,m.target_kind,m.target_id,count(*) n,min(m.ozon_account) account,min(m.ozon_product_id) product FROM product_mappings m JOIN relevant_targets t ON t.account=m.moysklad_account AND t.kind=m.target_kind AND t.id=m.target_id WHERE m.tenant_id=tenant GROUP BY m.moysklad_account,m.target_kind,m.target_id),
 key_usage AS MATERIALIZED(SELECT k.key,sum(u.n) n,min(u.account) account,min(u.product) product FROM target_keys k JOIN target_usage u ON u.target_account=moy AND u.target_kind=k.kind AND u.target_id=k.external_id GROUP BY k.key),
 joined AS(
 SELECT p.product_id,p.offer_id,COALESCE(p.name,'') name,p.sku,p.archived,p.key,p.duplicates,
 COALESCE(g.n,0) n,
 g.kind target_kind,g.id target_id,COALESCE(t.name,'') target_name,COALESCE(t.article,'') article,COALESCE(t.code,'') code,COALESCE(t.parent_id,'') parent_id,COALESCE(t.source_updated,'') source_updated,t.archived target_archived,
 m.id mapping_id,m.moysklad_account mapping_account,m.ozon_offer_id mapping_offer,m.target_kind mapping_kind,m.target_id mapping_target,om.id other_offer,
 CASE WHEN p.key='' THEN false ELSE COALESCE(u.n>1 OR (u.n=1 AND (u.account<>ozon OR u.product<>p.product_id)),false) OR COALESCE(ku.n>1 OR (ku.n=1 AND (ku.account<>ozon OR ku.product<>p.product_id)),false) END shared_inventory
 FROM products p LEFT JOIN grouped g ON g.key=p.key
 LEFT JOIN source_entities t ON t.import_id=mi AND t.kind=g.kind AND t.external_id=g.id
 LEFT JOIN product_mappings m ON m.tenant_id=tenant AND m.ozon_account=ozon AND m.ozon_product_id=p.product_id
 LEFT JOIN target_usage u ON u.target_account=m.moysklad_account AND u.target_kind=m.target_kind AND u.target_id=m.target_id
 LEFT JOIN key_usage ku ON ku.key=p.key
 LEFT JOIN product_mappings om ON om.tenant_id=tenant AND om.ozon_account=ozon AND om.ozon_offer_id=p.offer_id AND om.ozon_product_id<>p.product_id
 ), classified AS(
 SELECT j.*,CASE
 WHEN key='' THEN CASE WHEN duplicates>1 THEN ARRAY['conflict','duplicate_offer'] ELSE ARRAY['unmatched','no_match'] END
 WHEN mapping_id IS NOT NULL AND (mapping_account<>moy OR mapping_offer<>offer_id OR n>1 OR (n=1 AND (mapping_kind<>target_kind OR mapping_target<>target_id)) OR other_offer IS NOT NULL) THEN ARRAY['conflict','mapping_conflict']
 WHEN mapping_id IS NOT NULL THEN ARRAY['confirmed','already_confirmed']
 WHEN duplicates>1 THEN ARRAY['conflict','duplicate_offer']
 WHEN other_offer IS NOT NULL THEN ARRAY['conflict','mapping_conflict']
 WHEN n>1 THEN ARRAY['conflict','duplicate_sku']
 WHEN n=0 THEN ARRAY['unmatched','no_match']
 WHEN archived IS NULL OR target_archived IS NULL THEN ARRAY['review','lifecycle_unknown']
 WHEN archived OR target_archived THEN ARRAY['review','archived']
 WHEN target_kind='variant' OR parent_id<>'' THEN ARRAY['review','variant_requires_review']
 ELSE ARRAY['exact','exact_sku'] END result FROM joined j)
 SELECT product_id,offer_id,name,result[1],result[2],target_kind,target_id,
 CASE WHEN n=1 THEN jsonb_build_array(jsonb_build_object('id',target_id,'kind',target_kind,'name',target_name,'article',article,'evidence','exact_sku','score',1)) ELSE '[]'::jsonb END,
 shared_inventory,sku,archived,target_name,article,code,parent_id,source_updated FROM classified
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_matching_jobs WHERE apply_state<>'NOT_STARTED' OR criterion<>'article_or_code') OR EXISTS(SELECT 1 FROM product_matching_job_candidates) THEN
  RAISE EXCEPTION 'Bulk matching evidence exists; retain migration 78 and use a forward fix';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER saas_actor_product_review ON product_mapping_reviews;
DROP TRIGGER saas_actor_product_confirmation ON product_mapping_confirmation_audits;
CREATE TRIGGER saas_actor_product_review BEFORE INSERT ON product_mapping_reviews FOR EACH ROW EXECUTE FUNCTION saas_validate_audit_actor();
CREATE TRIGGER saas_actor_product_confirmation BEFORE INSERT ON product_mapping_confirmation_audits FOR EACH ROW EXECUTE FUNCTION saas_validate_audit_actor();
DROP FUNCTION product_matching_validate_actors();
ALTER TABLE product_matching_cycles DROP CONSTRAINT product_matching_cycle_evidence, DROP CONSTRAINT product_matching_cycle_job_scope, DROP COLUMN job_id, ALTER COLUMN run_id SET NOT NULL;
ALTER TABLE product_matching_jobs DROP CONSTRAINT product_matching_jobs_scope;
DROP TABLE product_matching_job_results;
DROP FUNCTION product_matching_review_id(text,text);
DROP FUNCTION product_matching_confirmation_hash(text,text);
DROP FUNCTION product_matching_observation_hash(bigint,bigint,text,text,boolean,text,text,text,text,text);
DROP FUNCTION product_matching_rows(text,text,text,bigint,bigint,text[]);
DROP FUNCTION product_matching_json(text);
DROP FUNCTION product_matching_escape(text);
DROP TABLE product_matching_job_candidates;
DROP INDEX product_matching_apply_queue;
ALTER TABLE product_matching_jobs DROP COLUMN criterion, DROP COLUMN apply_state, DROP COLUMN apply_key,
 DROP COLUMN apply_total, DROP COLUMN confirmed, DROP COLUMN skipped, DROP COLUMN apply_code;
