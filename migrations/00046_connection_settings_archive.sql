-- +goose Up
-- Registration and imported evidence remain immutable when a retired local
-- connection is removed from active navigation. No records are deleted.
ALTER TABLE connection_settings ADD COLUMN archived boolean NOT NULL DEFAULT false;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM connection_settings WHERE archived) THEN
  RAISE EXCEPTION 'refusing to reactivate archived connections during rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE connection_settings DROP COLUMN archived;
