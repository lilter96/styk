-- +goose Up
-- Existing grants keep their expiry. Only an explicit, audited config save may
-- create permanent mirror authority; NULL expiry alone never authorizes work.
ALTER TABLE orders_automation_grants ALTER COLUMN expires_at DROP NOT NULL;
ALTER TABLE orders_automation_grants ADD CONSTRAINT orders_grant_lifetime CHECK (
 (COALESCE((grant_record->>'perpetual')::boolean,false) AND expires_at IS NULL)
 OR (NOT COALESCE((grant_record->>'perpetual')::boolean,false) AND expires_at IS NOT NULL AND expires_at>granted_at)
);

-- +goose Down
-- Permanent grants and config revisions are immutable audit records. A
-- downgrade is safe only before the new authorization format has been used.
-- Disabling automation does not delete history; use a forward fix thereafter.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_automation_grants WHERE expires_at IS NULL)
 OR EXISTS(SELECT 1 FROM orders_config_revisions WHERE config#>>'{automation,perpetual}'='true') THEN
  RAISE EXCEPTION 'Permanent order authorization history exists; retain migration 69 and use a forward fix';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE orders_automation_grants DROP CONSTRAINT orders_grant_lifetime;
ALTER TABLE orders_automation_grants ALTER COLUMN expires_at SET NOT NULL;
