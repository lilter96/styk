-- +goose Up
-- Retain bounded, sanitized per-target provider results independently of HTTP.
ALTER TABLE stock_publication_targets ADD COLUMN provider_updated boolean;
ALTER TABLE stock_publication_targets ADD COLUMN provider_error_codes text[] NOT NULL DEFAULT '{}';
-- +goose StatementBegin
CREATE FUNCTION stock_safe_provider_codes(codes text[]) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT cardinality(codes)<=32 AND NOT EXISTS(SELECT 1 FROM unnest(codes) AS code WHERE code IS NULL OR code !~ '^[A-Z][A-Z0-9_]{0,63}$')
$$;
-- +goose StatementEnd
ALTER TABLE stock_publication_targets ADD CONSTRAINT stock_provider_result_sanitized CHECK(stock_safe_provider_codes(provider_error_codes) AND (provider_updated IS NOT NULL OR cardinality(provider_error_codes)=0));
-- +goose StatementBegin
CREATE FUNCTION stock_provider_result_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.provider_updated IS NOT NULL AND (NEW.provider_updated,NEW.provider_error_codes) IS DISTINCT FROM (OLD.provider_updated,OLD.provider_error_codes) THEN
  RAISE EXCEPTION 'provider target result is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER stock_provider_result_guard BEFORE UPDATE ON stock_publication_targets FOR EACH ROW EXECUTE FUNCTION stock_provider_result_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_publication_targets WHERE provider_updated IS NOT NULL OR cardinality(provider_error_codes)>0) THEN
  RAISE EXCEPTION 'refusing to discard provider result evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER stock_provider_result_guard ON stock_publication_targets;
DROP FUNCTION stock_provider_result_guard();
ALTER TABLE stock_publication_targets DROP CONSTRAINT stock_provider_result_sanitized;
ALTER TABLE stock_publication_targets DROP COLUMN provider_error_codes;
ALTER TABLE stock_publication_targets DROP COLUMN provider_updated;
DROP FUNCTION stock_safe_provider_codes(text[]);
