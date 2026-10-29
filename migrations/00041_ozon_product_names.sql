-- +goose Up
-- Name is display metadata from the actual product-info response. Historical
-- unknown names remain NULL, and names are never inventory-identity evidence.
ALTER TABLE ozon_product_observations ADD COLUMN name text CHECK(name IS NULL OR octet_length(name)<=4096);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ozon_product_observations WHERE name IS NOT NULL) THEN
  RAISE EXCEPTION 'refusing to discard observed Ozon product names';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE ozon_product_observations DROP COLUMN name;
