-- +goose Up
-- Supersession preserves historical UNKNOWN work. Only known native-document
-- updates can release a fence; uncertain creates and fulfillment stay fenced.
ALTER TABLE orders_previews ADD COLUMN recorded_at timestamptz;
-- Historical review receipt time was not recorded: retain NULL, never backfill.
ALTER TABLE orders_previews ALTER COLUMN recorded_at SET DEFAULT clock_timestamp();
CREATE TABLE orders_recovery_evidence (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
 work_id text NOT NULL,
 tenant_id text NOT NULL,moysklad_account text NOT NULL,ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 ozon_credential_revision bigint NOT NULL CHECK(ozon_credential_revision>0),
 moysklad_credential_revision bigint NOT NULL CHECK(moysklad_credential_revision>0),
 actor text NOT NULL,owner_user_id text NOT NULL,
 evidence jsonb NOT NULL CHECK(jsonb_typeof(evidence)='object' AND octet_length(evidence::text)<=2097152),
 created_at timestamptz NOT NULL,expires_at timestamptz NOT NULL CHECK(expires_at>created_at AND expires_at-created_at<=interval '2 minutes'),
 UNIQUE(id,work_id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES saas_merchant_accounts(tenant_id,user_id),
 CHECK((evidence->>'id'=id AND evidence->>'work_id'=work_id AND evidence->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account)) IS TRUE)
);
CREATE INDEX orders_recovery_evidence_history ON orders_recovery_evidence(work_id,created_at DESC,id DESC);
CREATE TRIGGER orders_recovery_evidence_actor BEFORE INSERT ON orders_recovery_evidence FOR EACH ROW EXECUTE FUNCTION orders_actor_guard();
CREATE TRIGGER orders_recovery_evidence_immutable BEFORE UPDATE OR DELETE ON orders_recovery_evidence FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TABLE orders_recovery_resolutions (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),work_id text NOT NULL UNIQUE,
 tenant_id text NOT NULL,moysklad_account text NOT NULL,ozon_account text NOT NULL,
 evidence_id text NOT NULL,
 native_kind text NOT NULL CHECK(native_kind='customerorder'),
 native_id text NOT NULL CHECK(native_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
 actor text NOT NULL,owner_user_id text NOT NULL,
 accepted_uncertainty boolean NOT NULL CHECK(accepted_uncertainty),worker_stopped boolean NOT NULL CHECK(worker_stopped),
 reason_digest text NOT NULL CHECK(reason_digest ~ '^[0-9a-f]{64}$'),
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL,
 UNIQUE(id,tenant_id,moysklad_account,ozon_account),
 UNIQUE(tenant_id,moysklad_account,ozon_account,request_key),
 FOREIGN KEY(evidence_id,work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_recovery_evidence(id,work_id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES saas_merchant_accounts(tenant_id,user_id)
);
CREATE TRIGGER orders_recovery_resolution_actor BEFORE INSERT ON orders_recovery_resolutions FOR EACH ROW EXECUTE FUNCTION orders_actor_guard();
CREATE TRIGGER orders_recovery_resolutions_immutable BEFORE UPDATE OR DELETE ON orders_recovery_resolutions FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TABLE orders_recovery_attestations (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),resolution_id text NOT NULL,work_id text NOT NULL,
 tenant_id text NOT NULL,moysklad_account text NOT NULL,ozon_account text NOT NULL,evidence_id text NOT NULL,
 actor text NOT NULL,owner_user_id text NOT NULL,
 accepted_uncertainty boolean NOT NULL CHECK(accepted_uncertainty),worker_stopped boolean NOT NULL CHECK(worker_stopped),
 reason_digest text NOT NULL CHECK(reason_digest ~ '^[0-9a-f]{64}$'),
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL,valid_until timestamptz NOT NULL CHECK(valid_until>created_at AND valid_until-created_at<=interval '2 minutes'),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account),UNIQUE(tenant_id,moysklad_account,ozon_account,request_key),
 FOREIGN KEY(resolution_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_recovery_resolutions(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(evidence_id,work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_recovery_evidence(id,work_id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES saas_merchant_accounts(tenant_id,user_id)
);
CREATE INDEX orders_recovery_attestation_history ON orders_recovery_attestations(resolution_id,created_at DESC,id DESC);
CREATE TRIGGER orders_recovery_attestation_actor BEFORE INSERT ON orders_recovery_attestations FOR EACH ROW EXECUTE FUNCTION orders_actor_guard();
CREATE TRIGGER orders_recovery_attestations_immutable BEFORE UPDATE OR DELETE ON orders_recovery_attestations FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TABLE orders_recovery_reason_secrets (
 attestation_id text PRIMARY KEY REFERENCES orders_recovery_attestations(id),
 ciphertext bytea NOT NULL CHECK(octet_length(ciphertext) BETWEEN 29 AND 16384),
 created_at timestamptz NOT NULL,expires_at timestamptz NOT NULL CHECK(expires_at>created_at AND expires_at-created_at<=interval '24 hours')
);
CREATE INDEX orders_recovery_reason_expiry ON orders_recovery_reason_secrets(expires_at,attestation_id);
CREATE TRIGGER orders_recovery_reason_no_update BEFORE UPDATE ON orders_recovery_reason_secrets FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TABLE orders_recovery_links (
 attestation_id text PRIMARY KEY,resolution_id text NOT NULL,work_id text NOT NULL UNIQUE,operation_id text NOT NULL,
 tenant_id text NOT NULL,moysklad_account text NOT NULL,ozon_account text NOT NULL,created_at timestamptz NOT NULL,
 FOREIGN KEY(attestation_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_recovery_attestations(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(resolution_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_recovery_resolutions(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(work_id,operation_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account)
);
CREATE TRIGGER orders_recovery_links_immutable BEFORE UPDATE OR DELETE ON orders_recovery_links FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TABLE orders_target_fences (
 tenant_id text NOT NULL,moysklad_account text NOT NULL,ozon_account text NOT NULL,posting_number text NOT NULL,work_id text NOT NULL UNIQUE,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,posting_number),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_work(id,tenant_id,moysklad_account,ozon_account)
);
INSERT INTO orders_target_fences SELECT tenant_id,moysklad_account,ozon_account,posting_number,id FROM orders_work WHERE state IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN');
DROP INDEX orders_work_active_target;
-- The independent global Ozon fulfillment fence from migration54 remains intact.
-- +goose StatementBegin
CREATE FUNCTION orders_target_fence_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND NEW.state IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN') THEN
  INSERT INTO orders_target_fences VALUES(NEW.tenant_id,NEW.moysklad_account,NEW.ozon_account,NEW.posting_number,NEW.id);
 ELSIF TG_OP='UPDATE' AND OLD.state IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN') AND NEW.state NOT IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN') THEN
  DELETE FROM orders_target_fences WHERE work_id=NEW.id;
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_target_fence_change AFTER INSERT OR UPDATE OF state ON orders_work FOR EACH ROW EXECUTE FUNCTION orders_target_fence_change();
-- +goose StatementBegin
CREATE FUNCTION orders_recovery_fence_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM orders_work WHERE id=OLD.work_id AND state IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN')) AND NOT EXISTS(SELECT 1 FROM orders_recovery_resolutions WHERE work_id=OLD.work_id) THEN
  RAISE EXCEPTION 'unknown order requires audited supersession' USING ERRCODE='55000';
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_recovery_fence_guard BEFORE DELETE ON orders_target_fences FOR EACH ROW EXECUTE FUNCTION orders_recovery_fence_guard();
CREATE TRIGGER orders_target_fences_no_update BEFORE UPDATE ON orders_target_fences FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
-- +goose StatementBegin
CREATE FUNCTION orders_target_fence_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM orders_work w WHERE w.id=NEW.work_id AND w.tenant_id=NEW.tenant_id AND w.moysklad_account=NEW.moysklad_account AND w.ozon_account=NEW.ozon_account AND w.posting_number=NEW.posting_number AND w.state IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN') AND NOT EXISTS(SELECT 1 FROM orders_recovery_resolutions WHERE work_id=w.id)) THEN RAISE EXCEPTION 'target fence must match current active work' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_target_fence_identity BEFORE INSERT ON orders_target_fences FOR EACH ROW EXECUTE FUNCTION orders_target_fence_identity();

-- +goose StatementBegin
CREATE FUNCTION orders_superseded_original_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.state='UNKNOWN' AND EXISTS(SELECT 1 FROM orders_recovery_resolutions WHERE work_id=OLD.id) AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'superseded UNKNOWN order is historical evidence' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_superseded_original_guard BEFORE UPDATE ON orders_work FOR EACH ROW EXECUTE FUNCTION orders_superseded_original_guard();
-- +goose StatementBegin
CREATE FUNCTION orders_recovery_resolution_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM orders_work w JOIN orders_target_fences f ON f.work_id=w.id JOIN outbox_tasks q ON q.orders_work_id=w.id AND q.task_type='orders_sync'
 JOIN orders_recovery_evidence e ON e.id=NEW.evidence_id AND e.work_id=w.id
 JOIN orders_configs c ON c.tenant_id=w.tenant_id AND c.moysklad_account=w.moysklad_account AND c.ozon_account=w.ozon_account
 JOIN orders_config_revisions cr ON cr.tenant_id=c.tenant_id AND cr.moysklad_account=c.moysklad_account AND cr.ozon_account=c.ozon_account AND cr.revision=c.revision
 JOIN orders_projection_bindings b ON b.tenant_id=w.tenant_id AND b.moysklad_account=w.moysklad_account AND b.ozon_account=w.ozon_account AND b.posting_number=w.posting_number
 JOIN orders_posting_heads h ON h.tenant_id=w.tenant_id AND h.moysklad_account=w.moysklad_account AND h.ozon_account=w.ozon_account AND h.posting_number=w.posting_number
 JOIN connection_credentials mo ON mo.tenant_id=w.tenant_id AND mo.provider='moysklad' AND mo.account_id=w.moysklad_account
 JOIN connection_credentials oz ON oz.tenant_id=w.tenant_id AND oz.provider='ozon' AND oz.account_id=w.ozon_account
 WHERE w.id=NEW.work_id AND w.tenant_id=NEW.tenant_id AND w.moysklad_account=NEW.moysklad_account AND w.ozon_account=NEW.ozon_account
 AND w.state='UNKNOWN' AND w.dispatch_count=1 AND w.action IN ('mirror_update','mirror_position_update')
 AND w.work#>>'{command,correlation,customer_order_id}'=NEW.native_id
 AND q.state='pending' AND q.lease_until IS NULL AND c.enabled AND cr.config->>'test_enabled'='true' AND c.revision=e.config_revision
 AND b.customer_order_id=NEW.native_id AND NOT h.quarantined AND (e.evidence->>'stored_source_revision')=(h.projection#>>'{evidence,revision}')
 AND h.checked_at<=clock_timestamp()+interval '1 minute' AND clock_timestamp()-h.checked_at<=make_interval(secs=>(cr.config->>'source_freshness_seconds')::integer)
 AND e.ozon_credential_revision=oz.revision AND e.moysklad_credential_revision=mo.revision
 AND e.created_at<=clock_timestamp() AND e.expires_at>clock_timestamp()
 AND e.evidence#>>'{target,kind}'=NEW.native_kind AND e.evidence#>>'{target,correlation,customer_order_id}'=NEW.native_id
 AND e.evidence#>>'{target,complete}'='true' AND jsonb_typeof(e.evidence->'source')='object') THEN
 RAISE EXCEPTION 'recovery requires fresh exact known-native evidence' USING ERRCODE='23514'; END IF;
 IF EXISTS(SELECT 1 FROM provider_call_permits p WHERE p.tenant_id=NEW.tenant_id AND ((p.provider='moysklad' AND p.account_id=NEW.moysklad_account) OR (p.provider='ozon' AND p.account_id=NEW.ozon_account)) AND p.state IN ('reserved','sending') AND p.lease_until>clock_timestamp()) THEN
 RAISE EXCEPTION 'active provider admission blocks order recovery' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_recovery_resolution_guard BEFORE INSERT ON orders_recovery_resolutions FOR EACH ROW EXECUTE FUNCTION orders_recovery_resolution_guard();
-- +goose StatementBegin
CREATE FUNCTION orders_recovery_new_work_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r orders_recovery_resolutions; a orders_recovery_attestations;
BEGIN
 SELECT resolution.* INTO r FROM orders_recovery_resolutions resolution JOIN orders_work prior_work ON prior_work.id=resolution.work_id
 WHERE resolution.tenant_id=NEW.tenant_id AND resolution.moysklad_account=NEW.moysklad_account AND resolution.ozon_account=NEW.ozon_account AND prior_work.posting_number=NEW.posting_number ORDER BY resolution.created_at DESC LIMIT 1;
 IF NOT FOUND THEN RETURN NULL; END IF;
 IF EXISTS(SELECT 1 FROM orders_recovery_links l JOIN orders_reconciliations rec ON rec.work_id=l.work_id WHERE l.resolution_id=r.id) THEN RETURN NULL; END IF;
 SELECT attestation.* INTO a FROM orders_recovery_attestations attestation WHERE attestation.resolution_id=r.id ORDER BY created_at DESC,id DESC LIMIT 1;
 IF NOT FOUND OR a.valid_until<=clock_timestamp() OR EXISTS(SELECT 1 FROM orders_recovery_links WHERE attestation_id=a.id) THEN
  RAISE EXCEPTION 'fresh recovery attestation required' USING ERRCODE='23514'; END IF;
 IF NEW.action NOT IN ('mirror_update','mirror_position_update') OR NEW.work#>>'{command,correlation,customer_order_id}' IS DISTINCT FROM r.native_id OR NOT EXISTS(SELECT 1 FROM orders_previews p JOIN orders_operations o ON o.preview_id=p.id WHERE o.id=NEW.operation_id AND p.recorded_at>a.created_at AND o.created_at>=p.created_at) THEN
  RAISE EXCEPTION 'supersession needs a fresh exact-native review' USING ERRCODE='23514'; END IF;
 INSERT INTO orders_recovery_links VALUES(a.id,r.id,NEW.id,NEW.operation_id,NEW.tenant_id,NEW.moysklad_account,NEW.ozon_account,clock_timestamp());
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_recovery_new_work_guard AFTER INSERT ON orders_work FOR EACH ROW EXECUTE FUNCTION orders_recovery_new_work_guard();
-- +goose StatementBegin
CREATE FUNCTION orders_recovery_link_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM orders_recovery_resolutions r JOIN orders_recovery_attestations a ON a.resolution_id=r.id AND a.id=NEW.attestation_id JOIN orders_work w ON w.id=NEW.work_id AND w.operation_id=NEW.operation_id JOIN orders_operations o ON o.id=w.operation_id JOIN orders_previews p ON p.id=o.preview_id WHERE r.id=NEW.resolution_id AND r.tenant_id=NEW.tenant_id AND r.moysklad_account=NEW.moysklad_account AND r.ozon_account=NEW.ozon_account AND w.tenant_id=r.tenant_id AND w.moysklad_account=r.moysklad_account AND w.ozon_account=r.ozon_account AND w.action IN ('mirror_update','mirror_position_update') AND w.work#>>'{command,correlation,customer_order_id}'=r.native_id AND p.recorded_at>a.created_at AND a.valid_until>clock_timestamp()) THEN
 RAISE EXCEPTION 'recovery link requires fresh exact-native work' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_recovery_link_guard BEFORE INSERT ON orders_recovery_links FOR EACH ROW EXECUTE FUNCTION orders_recovery_link_guard();
-- +goose StatementBegin
CREATE FUNCTION orders_recovery_attestation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM orders_recovery_attestations a WHERE a.resolution_id=NEW.resolution_id AND a.valid_until>clock_timestamp() AND NOT EXISTS(SELECT 1 FROM orders_recovery_links l WHERE l.attestation_id=a.id)) THEN RAISE EXCEPTION 'current unused recovery attestation remains valid' USING ERRCODE='23514'; END IF;
 IF EXISTS(SELECT 1 FROM provider_call_permits p WHERE p.tenant_id=NEW.tenant_id AND ((p.provider='moysklad' AND p.account_id=NEW.moysklad_account) OR (p.provider='ozon' AND p.account_id=NEW.ozon_account)) AND p.state IN ('reserved','sending') AND p.lease_until>clock_timestamp()) THEN RAISE EXCEPTION 'active provider admission blocks recovery attestation' USING ERRCODE='23514'; END IF;
 IF NOT EXISTS(SELECT 1 FROM orders_recovery_evidence e JOIN orders_work w ON w.id=e.work_id JOIN orders_posting_heads h ON h.tenant_id=e.tenant_id AND h.moysklad_account=e.moysklad_account AND h.ozon_account=e.ozon_account AND h.posting_number=w.posting_number WHERE e.id=NEW.evidence_id AND NOT h.quarantined AND e.evidence->>'stored_source_revision'=h.projection#>>'{evidence,revision}' AND NOT EXISTS(SELECT 1 FROM orders_recovery_evidence later WHERE later.work_id=e.work_id AND (later.created_at,later.id)>(e.created_at,e.id))) THEN RAISE EXCEPTION 'latest current source evidence required for recovery attestation' USING ERRCODE='23514'; END IF;
 IF NOT EXISTS(SELECT 1 FROM orders_recovery_resolutions r JOIN orders_recovery_evidence e ON e.id=NEW.evidence_id AND e.work_id=r.work_id JOIN orders_work w ON w.id=r.work_id JOIN outbox_tasks q ON q.orders_work_id=w.id AND q.task_type='orders_sync' JOIN orders_configs c ON c.tenant_id=r.tenant_id AND c.moysklad_account=r.moysklad_account AND c.ozon_account=r.ozon_account JOIN orders_config_revisions cr ON cr.tenant_id=c.tenant_id AND cr.moysklad_account=c.moysklad_account AND cr.ozon_account=c.ozon_account AND cr.revision=c.revision JOIN connection_credentials mo ON mo.tenant_id=r.tenant_id AND mo.provider='moysklad' AND mo.account_id=r.moysklad_account JOIN connection_credentials oz ON oz.tenant_id=r.tenant_id AND oz.provider='ozon' AND oz.account_id=r.ozon_account
 WHERE r.id=NEW.resolution_id AND r.work_id=NEW.work_id AND r.tenant_id=NEW.tenant_id AND r.moysklad_account=NEW.moysklad_account AND r.ozon_account=NEW.ozon_account AND w.state='UNKNOWN' AND w.action IN ('mirror_update','mirror_position_update') AND w.work#>>'{command,correlation,customer_order_id}'=r.native_id AND e.evidence#>>'{target,correlation,customer_order_id}'=r.native_id AND e.evidence#>>'{target,complete}'='true' AND jsonb_typeof(e.evidence->'source')='object' AND c.enabled AND cr.config->>'test_enabled'='true' AND c.revision=e.config_revision AND mo.revision=e.moysklad_credential_revision AND oz.revision=e.ozon_credential_revision AND e.expires_at>clock_timestamp() AND NEW.valid_until=e.expires_at AND q.state IN ('pending','failed') AND q.lease_until IS NULL) THEN
 RAISE EXCEPTION 'attestation requires fresh scoped known-native evidence' USING ERRCODE='23514'; END IF;
 IF EXISTS(SELECT 1 FROM orders_target_fences f JOIN orders_work prior_work ON prior_work.id=NEW.work_id WHERE f.tenant_id=NEW.tenant_id AND f.moysklad_account=NEW.moysklad_account AND f.ozon_account=NEW.ozon_account AND f.posting_number=prior_work.posting_number AND f.work_id<>NEW.work_id) OR EXISTS(SELECT 1 FROM orders_recovery_links l JOIN orders_work newer ON newer.id=l.work_id WHERE l.resolution_id=NEW.resolution_id AND (newer.dispatch_count>0 OR newer.state<>'BLOCKED')) THEN
 RAISE EXCEPTION 'new recovery decision must be investigated before renewal' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER orders_recovery_attestation_guard BEFORE INSERT ON orders_recovery_attestations FOR EACH ROW EXECUTE FUNCTION orders_recovery_attestation_guard();
-- +goose StatementBegin
CREATE FUNCTION orders_recovery_reason_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.expires_at>clock_timestamp() THEN RAISE EXCEPTION 'recovery reason retention has not expired' USING ERRCODE='55000'; END IF;
  RETURN OLD;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM orders_recovery_reason_secrets WHERE attestation_id=NEW.id) THEN RAISE EXCEPTION 'recovery reason must be encrypted' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER orders_recovery_reason_required AFTER INSERT ON orders_recovery_attestations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION orders_recovery_reason_guard();
CREATE TRIGGER orders_recovery_reason_retention BEFORE DELETE ON orders_recovery_reason_secrets FOR EACH ROW EXECUTE FUNCTION orders_recovery_reason_guard();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION orders_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE wid text; ws text; qs text; wc integer; recovered boolean;
BEGIN
 IF TG_TABLE_NAME='outbox_tasks' THEN IF NEW.task_type<>'orders_sync' THEN RETURN NULL; END IF; wid:=NEW.orders_work_id; ELSE wid:=NEW.id; END IF;
 SELECT w.state,w.dispatch_count,q.state,EXISTS(SELECT 1 FROM orders_recovery_resolutions r WHERE r.work_id=w.id) INTO ws,wc,qs,recovered FROM orders_work w JOIN outbox_tasks q ON q.orders_work_id=w.id AND q.task_type='orders_sync' WHERE w.id=wid;
 IF NOT FOUND THEN RAISE EXCEPTION 'order work requires its outbox task' USING ERRCODE='23514'; END IF;
 IF ws='RECONCILED' AND (qs<>'succeeded' OR NOT EXISTS(SELECT 1 FROM orders_reconciliations r WHERE r.work_id=wid)) THEN RAISE EXCEPTION 'order reconciliation requires durable readback' USING ERRCODE='23514'; END IF;
 IF ws IN ('REJECTED','BLOCKED') AND qs<>'failed' THEN RAISE EXCEPTION 'order failure requires failed task' USING ERRCODE='23514'; END IF;
 IF recovered AND (ws<>'UNKNOWN' OR qs<>'failed' OR EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.work_id=wid)) THEN RAISE EXCEPTION 'superseded order must preserve UNKNOWN with stopped readback' USING ERRCODE='23514'; END IF;
 IF NOT recovered AND ws IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN') AND qs NOT IN ('pending','running') THEN RAISE EXCEPTION 'order active work requires active task' USING ERRCODE='23514'; END IF;
 IF wc=1 AND (NOT EXISTS(SELECT 1 FROM orders_attempts a WHERE a.work_id=wid AND a.phase='DISPATCHING') OR NOT EXISTS(SELECT 1 FROM orders_write_intents i WHERE i.work_id=wid)) THEN RAISE EXCEPTION 'order dispatch requires exact immutable intent' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM orders_recovery_resolutions) OR EXISTS(SELECT 1 FROM orders_recovery_evidence) THEN RAISE EXCEPTION 'refusing to discard order recovery audit'; END IF; END $$;
-- +goose StatementEnd
DROP TRIGGER orders_recovery_new_work_guard ON orders_work;
DROP FUNCTION orders_recovery_new_work_guard();
DROP TRIGGER orders_recovery_resolution_guard ON orders_recovery_resolutions;
DROP FUNCTION orders_recovery_resolution_guard();
DROP TRIGGER orders_superseded_original_guard ON orders_work;
DROP FUNCTION orders_superseded_original_guard();
DROP TRIGGER orders_recovery_fence_guard ON orders_target_fences;
DROP FUNCTION orders_recovery_fence_guard();
DROP TRIGGER orders_target_fence_change ON orders_work;
DROP FUNCTION orders_target_fence_change();
DROP TABLE orders_target_fences;
DROP FUNCTION orders_target_fence_identity();
CREATE UNIQUE INDEX orders_work_active_target ON orders_work(tenant_id,moysklad_account,ozon_account,posting_number) WHERE state IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN');
DROP TABLE orders_recovery_reason_secrets,orders_recovery_links,orders_recovery_attestations,orders_recovery_resolutions,orders_recovery_evidence;
DROP FUNCTION orders_recovery_attestation_guard(),orders_recovery_reason_guard();
DROP FUNCTION orders_recovery_link_guard();
ALTER TABLE orders_previews DROP COLUMN recorded_at;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION orders_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE wid text; ws text; qs text; wc integer;
BEGIN
 IF TG_TABLE_NAME='outbox_tasks' THEN IF NEW.task_type<>'orders_sync' THEN RETURN NULL; END IF; wid:=NEW.orders_work_id; ELSE wid:=NEW.id; END IF;
 SELECT w.state,w.dispatch_count,q.state INTO ws,wc,qs FROM orders_work w JOIN outbox_tasks q ON q.orders_work_id=w.id AND q.task_type='orders_sync' WHERE w.id=wid;
 IF NOT FOUND THEN RAISE EXCEPTION 'order work requires its outbox task' USING ERRCODE='23514'; END IF;
 IF ws='RECONCILED' AND (qs<>'succeeded' OR NOT EXISTS(SELECT 1 FROM orders_reconciliations r WHERE r.work_id=wid)) THEN RAISE EXCEPTION 'order reconciliation requires durable readback' USING ERRCODE='23514'; END IF;
 IF ws IN ('REJECTED','BLOCKED') AND qs<>'failed' THEN RAISE EXCEPTION 'order failure requires failed task' USING ERRCODE='23514'; END IF;
 IF ws IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN') AND qs NOT IN ('pending','running') THEN RAISE EXCEPTION 'order active work requires active task' USING ERRCODE='23514'; END IF;
 IF wc=1 AND (NOT EXISTS(SELECT 1 FROM orders_attempts a WHERE a.work_id=wid AND a.phase='DISPATCHING') OR NOT EXISTS(SELECT 1 FROM orders_write_intents i WHERE i.work_id=wid)) THEN RAISE EXCEPTION 'order dispatch requires exact immutable intent' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
