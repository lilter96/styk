-- +goose Up
-- The absence of a stock row is not a zero balance. An operator's separate,
-- scoped takeover allows one first publication, recorded before dispatch.
-- A later absent row cannot silently authorize another first publication.
CREATE TABLE stock_initial_publications (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 product_id text NOT NULL,
 warehouse_id text NOT NULL,
 config_revision bigint NOT NULL,
 publication_id text NOT NULL UNIQUE REFERENCES stock_publications(id),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(ozon_account,product_id,warehouse_id),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision)
  REFERENCES stock_auto_config_audits(tenant_id,moysklad_account,ozon_account,revision)
);
CREATE TRIGGER stock_initial_publications_immutable BEFORE UPDATE OR DELETE ON stock_initial_publications
 FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_initial_publications) THEN
  RAISE EXCEPTION 'refusing to discard initial publication authorization history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE stock_initial_publications;
