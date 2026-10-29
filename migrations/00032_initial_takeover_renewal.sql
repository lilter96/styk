-- +goose Up
-- A cancelled, never-dispatched intent can be replaced only by a newly
-- acknowledged policy revision. Every previous intent remains immutable.
ALTER TABLE stock_initial_publications DROP CONSTRAINT stock_initial_publications_pkey;
ALTER TABLE stock_initial_publications ADD PRIMARY KEY(ozon_account,product_id,warehouse_id,config_revision);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_initial_publications GROUP BY ozon_account,product_id,warehouse_id HAVING count(*)>1) THEN
  RAISE EXCEPTION 'refusing to discard renewed initial publication history';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE stock_initial_publications DROP CONSTRAINT stock_initial_publications_pkey;
ALTER TABLE stock_initial_publications ADD PRIMARY KEY(ozon_account,product_id,warehouse_id);
