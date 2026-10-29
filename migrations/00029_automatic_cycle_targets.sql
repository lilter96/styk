-- +goose Up
-- One account-level traversal may make independent publication decisions.
-- Existing cycles retain their historical single-publication association.
CREATE TABLE stock_auto_cycle_publications (
 cycle_id text NOT NULL REFERENCES stock_auto_cycles(id),
 publication_id text NOT NULL UNIQUE REFERENCES stock_publications(id),
 product_id text NOT NULL, warehouse_id text NOT NULL,
 outcome text NOT NULL DEFAULT '' CHECK(outcome IN ('','RECONCILED','PUBLICATION_UNKNOWN','PUBLICATION_REJECTED','RECONCILIATION_PENDING')),
 PRIMARY KEY(cycle_id,product_id,warehouse_id)
);
INSERT INTO stock_auto_cycle_publications(cycle_id,publication_id,product_id,warehouse_id,outcome)
 SELECT c.id,c.publication_id,t.product_id,t.warehouse_id,
 CASE WHEN c.outcome IN ('RECONCILED','PUBLICATION_UNKNOWN','PUBLICATION_REJECTED','RECONCILIATION_PENDING') THEN c.outcome ELSE '' END
 FROM stock_auto_cycles c JOIN stock_publication_targets t ON t.operation_id=c.publication_id
 WHERE c.publication_id IS NOT NULL;
-- +goose StatementBegin
CREATE FUNCTION stock_auto_cycle_publication_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'automatic publication association is immutable'; END IF;
 IF (NEW.cycle_id,NEW.publication_id,NEW.product_id,NEW.warehouse_id) IS DISTINCT FROM (OLD.cycle_id,OLD.publication_id,OLD.product_id,OLD.warehouse_id) OR (OLD.outcome<>'' AND NEW.outcome IS DISTINCT FROM OLD.outcome) THEN
  RAISE EXCEPTION 'automatic publication association is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER stock_auto_cycle_publication_guard BEFORE UPDATE OR DELETE ON stock_auto_cycle_publications FOR EACH ROW EXECUTE FUNCTION stock_auto_cycle_publication_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_auto_cycle_publications p JOIN stock_auto_cycles c ON c.id=p.cycle_id WHERE c.publication_id IS DISTINCT FROM p.publication_id) THEN
  RAISE EXCEPTION 'refusing to discard multi-target cycle publication history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE stock_auto_cycle_publications;
DROP FUNCTION stock_auto_cycle_publication_guard();
