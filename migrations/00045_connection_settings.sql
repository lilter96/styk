-- +goose Up
-- Descriptive metadata has its own revision. Credential revisions are bound
-- into encrypted-secret AAD and must never change for a display-name edit.
CREATE TABLE connection_settings (
 tenant_id text NOT NULL,
 source text NOT NULL CHECK(source='moysklad'),
 account_id text NOT NULL,
 name text NOT NULL CHECK(char_length(name) BETWEEN 1 AND 120 AND name=btrim(name) AND name !~ '[[:cntrl:]]'),
 description text NOT NULL DEFAULT '' CHECK(char_length(description)<=1000 AND description=btrim(description) AND description !~ '[[:cntrl:]]'),
 revision bigint NOT NULL CHECK(revision>0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_by text NOT NULL CHECK(length(updated_by)>0),
 PRIMARY KEY(tenant_id,source,account_id),
 FOREIGN KEY(tenant_id,source,account_id) REFERENCES stock_connections(tenant_id,source,account_id)
);

-- +goose Down
-- Rollback deliberately refuses to discard saved merchant settings.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM connection_settings) THEN
  RAISE EXCEPTION 'refusing to discard saved connection settings';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE connection_settings;
