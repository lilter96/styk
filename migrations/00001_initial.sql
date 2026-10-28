-- +goose Up
CREATE TABLE inventory_observations (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 operation_key text NOT NULL UNIQUE,
 source text NOT NULL,
 sku text NOT NULL,
 quantity integer NOT NULL CHECK (quantity >= 0),
 observed_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE outbox_tasks (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 operation_key text NOT NULL UNIQUE,
 observation_id bigint NOT NULL REFERENCES inventory_observations(id),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','running','succeeded','failed')),
 attempts integer NOT NULL DEFAULT 0,
 max_attempts integer NOT NULL CHECK (max_attempts > 0),
 available_at timestamptz NOT NULL DEFAULT now(),
 lease_until timestamptz,
 claim_token text,
 result text,
 last_error text,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_claim_idx ON outbox_tasks (available_at, id) WHERE state IN ('pending','running');
-- +goose Down
DROP TABLE outbox_tasks;
DROP TABLE inventory_observations;
