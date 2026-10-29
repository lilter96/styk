-- +goose Up
-- Recovery remains restricted to the registered test account pair. A new
-- target is eligible only when its original operation belongs to an immutable
-- cycle and an enabled, operator-audited policy that enrolled that exact pair.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION stock_test_recovery_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM stock_publication_targets t JOIN stock_publications p ON p.id=t.operation_id
  JOIN stock_publication_readbacks r ON r.id=NEW.readback_id AND r.target_id=t.id
  JOIN outbox_tasks q ON q.publication_operation_id=p.id
  JOIN stock_publication_target_fences f ON f.target_id=t.id
  WHERE t.id=NEW.target_id AND t.state='UNKNOWN' AND q.state='failed' AND q.attempts=1
   AND p.tenant_id=NEW.tenant_id AND p.moysklad_account=NEW.moysklad_account AND p.ozon_account=NEW.ozon_account
   AND t.product_id=NEW.product_id AND t.warehouse_id=NEW.warehouse_id
   AND NEW.tenant_id='cross-e2e' AND NEW.moysklad_account='2928cf35-d30a-11f1-0a80-1b9000000355' AND NEW.ozon_account='1549477'
   AND ((NEW.product_id='2764544165' AND NEW.warehouse_id='1020001323428000') OR EXISTS(
    SELECT 1 FROM stock_auto_cycle_publications cp JOIN stock_auto_cycles c ON c.id=cp.cycle_id
    JOIN stock_auto_config_audits a ON a.tenant_id=c.tenant_id AND a.moysklad_account=c.moysklad_account AND a.ozon_account=c.ozon_account AND a.revision=c.config_revision
    WHERE cp.publication_id=p.id AND cp.product_id=NEW.product_id AND cp.warehouse_id=NEW.warehouse_id
     AND c.tenant_id=NEW.tenant_id AND c.moysklad_account=NEW.moysklad_account AND c.ozon_account=NEW.ozon_account AND a.enabled
     AND a.snapshot->'targets' @> jsonb_build_array(jsonb_build_object('ozon_product_id',NEW.product_id,'ozon_warehouse_id',NEW.warehouse_id))
   ))
   AND r.quantity=NEW.observed_quantity AND r.observed_at=NEW.observed_at AND r.outcome IN ('MATCH','MISMATCH')
   AND r.id=(SELECT id FROM stock_publication_readbacks WHERE target_id=t.id ORDER BY observed_at DESC,id DESC LIMIT 1)
   AND NEW.created_at>=r.observed_at AND NEW.created_at-r.observed_at<=interval '2 minutes'
 ) THEN RAISE EXCEPTION 'invalid scoped recovery evidence'; END IF;
 IF EXISTS(SELECT 1 FROM stock_test_worker_presence w WHERE w.tenant_id=NEW.tenant_id AND w.moysklad_account=NEW.moysklad_account AND w.ozon_account=NEW.ozon_account AND w.expires_at>NEW.created_at)
  OR EXISTS(SELECT 1 FROM stock_publication_targets t WHERE t.ozon_account=NEW.ozon_account AND t.product_id=NEW.product_id AND t.warehouse_id=NEW.warehouse_id AND t.state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED')) THEN
  RAISE EXCEPTION 'active test worker or dispatch blocks recovery';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_publication_recoveries WHERE product_id<>'2764544165' OR warehouse_id<>'1020001323428000') THEN
  RAISE EXCEPTION 'refusing to revert enrolled-target recovery history';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION stock_test_recovery_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM stock_publication_targets t JOIN stock_publications p ON p.id=t.operation_id
  JOIN stock_publication_readbacks r ON r.id=NEW.readback_id AND r.target_id=t.id
  JOIN outbox_tasks q ON q.publication_operation_id=p.id
  JOIN stock_publication_target_fences f ON f.target_id=t.id
  WHERE t.id=NEW.target_id AND t.state='UNKNOWN' AND q.state='failed' AND q.attempts=1
   AND p.tenant_id=NEW.tenant_id AND p.moysklad_account=NEW.moysklad_account AND p.ozon_account=NEW.ozon_account
   AND t.product_id=NEW.product_id AND t.warehouse_id=NEW.warehouse_id
   AND NEW.tenant_id='cross-e2e' AND NEW.moysklad_account='2928cf35-d30a-11f1-0a80-1b9000000355'
   AND NEW.ozon_account='1549477' AND NEW.product_id='2764544165' AND NEW.warehouse_id='1020001323428000'
   AND r.quantity=NEW.observed_quantity AND r.observed_at=NEW.observed_at AND r.outcome IN ('MATCH','MISMATCH')
   AND r.id=(SELECT id FROM stock_publication_readbacks WHERE target_id=t.id ORDER BY observed_at DESC,id DESC LIMIT 1)
   AND NEW.created_at>=r.observed_at AND NEW.created_at-r.observed_at<=interval '2 minutes'
 ) THEN RAISE EXCEPTION 'invalid scoped recovery evidence'; END IF;
 IF EXISTS(SELECT 1 FROM stock_test_worker_presence w WHERE w.tenant_id=NEW.tenant_id AND w.moysklad_account=NEW.moysklad_account AND w.ozon_account=NEW.ozon_account AND w.expires_at>NEW.created_at)
  OR EXISTS(SELECT 1 FROM stock_publication_targets t WHERE t.ozon_account=NEW.ozon_account AND t.product_id=NEW.product_id AND t.warehouse_id=NEW.warehouse_id AND t.state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED')) THEN
  RAISE EXCEPTION 'active test worker or dispatch blocks recovery';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
