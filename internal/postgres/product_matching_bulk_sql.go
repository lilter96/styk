package postgres

// All new links, review evidence, history, exact-retry receipts and the job result
// are written atomically. Existing mappings are never updated or replaced.
const productMatchingApplySQL = `WITH job AS MATERIALIZED(SELECT * FROM product_matching_jobs WHERE id=$1),
 current AS MATERIALIZED(SELECT r.* FROM job j CROSS JOIN LATERAL product_matching_rows(j.tenant_id,j.moysklad_account,j.ozon_account,j.moy_import,j.ozon_import,$2) r
 JOIN product_matching_job_candidates frozen ON frozen.job_id=j.id AND frozen.product_id=r.product_id AND frozen.status='exact' AND r.status='exact' AND frozen.target_kind=r.target_kind AND frozen.target_id=r.target_id),
 records AS MATERIALIZED(SELECT r.*,product_matching_review_id($1,r.product_id) review_id,
 product_matching_observation_hash(j.ozon_import,j.moy_import,r.offer_id,r.sku,r.archived,r.target_name,r.article,r.code,r.parent_id,r.source_updated) observation_hash
 FROM current r CROSS JOIN job j),
 reviews AS(INSERT INTO product_mapping_reviews(id,tenant_id,ozon_account,moysklad_account,ozon_product_id,target_kind,target_id,ozon_import_id,moysklad_import_id,existing_mapping_id,existing_mapping_version,observation_hash,mapping_hash,evidence_class,principal_type,session_audit_id,created_at,expires_at,ozon_import_fingerprint,moysklad_import_fingerprint)
 SELECT r.review_id,j.tenant_id,j.ozon_account,j.moysklad_account,r.product_id,r.target_kind,r.target_id,j.ozon_import,j.moy_import,NULL,NULL,r.observation_hash,$4,'DETERMINISTIC_CANDIDATE',$3,j.actor,statement_timestamp(),statement_timestamp()+interval '5 minutes',o.fingerprint,m.fingerprint
 FROM records r CROSS JOIN job j JOIN source_imports o ON o.id=j.ozon_import JOIN source_imports m ON m.id=j.moy_import RETURNING id),
 mappings AS(INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance,confirmed_at)
 SELECT j.tenant_id,j.ozon_account,r.product_id,r.offer_id,j.moysklad_account,r.target_kind,r.target_id,1,$3,statement_timestamp() FROM records r CROSS JOIN job j WHERE (SELECT count(*) FROM reviews)>=0 ON CONFLICT DO NOTHING RETURNING *),
 history AS(INSERT INTO product_mapping_history(mapping_id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,ozon_import_id,moysklad_import_id,confirmation_review_id)
 SELECT m.id,m.version,m.tenant_id,m.ozon_account,m.ozon_product_id,m.ozon_offer_id,m.moysklad_account,m.target_kind,m.target_id,m.provenance,m.confirmed_at,j.ozon_import,j.moy_import,product_matching_review_id(j.id,m.ozon_product_id) FROM mappings m CROSS JOIN job j RETURNING *),
 audits AS(INSERT INTO product_mapping_confirmation_audits(mapping_id,mapping_version,review_id,tenant_id,ozon_account,moysklad_account,ozon_product_id,ozon_offer_id,target_kind,target_id,ozon_import_id,moysklad_import_id,evidence_class,principal_type,session_audit_id,explanation,confirmed_at)
 SELECT h.mapping_id,h.version,h.confirmation_review_id,h.tenant_id,h.ozon_account,h.moysklad_account,h.ozon_product_id,h.ozon_offer_id,h.target_kind,h.target_id,h.ozon_import_id,h.moysklad_import_id,'DETERMINISTIC_CANDIDATE',$3,j.actor,$5,h.confirmed_at FROM history h CROSS JOIN job j RETURNING mapping_id,mapping_version,review_id,session_audit_id),
 receipts AS(INSERT INTO product_mapping_confirmation_idempotency(review_id,session_audit_id,idempotency_key,request_hash,mapping_id,mapping_version)
 SELECT a.review_id,a.session_audit_id,j.apply_key,product_matching_confirmation_hash(a.review_id,$5),a.mapping_id,a.mapping_version FROM audits a CROSS JOIN job j RETURNING mapping_id),
 result AS(INSERT INTO product_matching_job_results(job_id,actor,request_key,total,confirmed,skipped)
 SELECT j.id,j.actor,j.apply_key,j.apply_total,(SELECT count(*) FROM receipts),j.apply_total-(SELECT count(*) FROM receipts) FROM job j RETURNING total,confirmed,skipped),
 updated AS(UPDATE product_matching_jobs j SET apply_state='COMPLETED',confirmed=r.confirmed,skipped=r.skipped,apply_code='',updated_at=clock_timestamp() FROM result r WHERE j.id=$1 RETURNING j.apply_total,j.confirmed,j.skipped)
 SELECT apply_total,confirmed,skipped FROM updated`
