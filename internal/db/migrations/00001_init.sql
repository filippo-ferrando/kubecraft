-- +goose Up
CREATE TABLE users (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username             text NOT NULL UNIQUE,
    password_hash        text NOT NULL,
    role                 text NOT NULL CHECK (role IN ('admin', 'user')),
    disabled             boolean NOT NULL DEFAULT false,
    must_change_password boolean NOT NULL DEFAULT false,
    console_enabled      boolean NOT NULL DEFAULT true,
    created_at           timestamptz NOT NULL DEFAULT now()
);

-- id is an HMAC of the cookie token, so a leaked table does not leak sessions.
CREATE TABLE sessions (
    id         text PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf_token text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    ip         text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE plans (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL UNIQUE,
    cpu_request_m   integer NOT NULL CHECK (cpu_request_m > 0),
    cpu_limit_m     integer NOT NULL CHECK (cpu_limit_m >= cpu_request_m),
    memory_limit_mb integer NOT NULL CHECK (memory_limit_mb >= 512),
    storage_gb      integer NOT NULL CHECK (storage_gb > 0),
    active          boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- Single row with the global default quota. NULL means unlimited.
CREATE TABLE default_quota (
    id                   boolean PRIMARY KEY DEFAULT true CHECK (id),
    max_servers          integer CHECK (max_servers >= 0),
    max_total_cpu_m      integer CHECK (max_total_cpu_m >= 0),
    max_total_memory_mb  integer CHECK (max_total_memory_mb >= 0),
    max_total_storage_gb integer CHECK (max_total_storage_gb >= 0)
);

-- Per-user overrides. NULL means use the default quota.
CREATE TABLE user_quotas (
    user_id              uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    max_servers          integer CHECK (max_servers >= 0),
    max_total_cpu_m      integer CHECK (max_total_cpu_m >= 0),
    max_total_memory_mb  integer CHECK (max_total_memory_mb >= 0),
    max_total_storage_gb integer CHECK (max_total_storage_gb >= 0)
);

CREATE TABLE servers (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id       uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    slug           text NOT NULL UNIQUE,
    display_name   text NOT NULL,
    plan_id        uuid NOT NULL REFERENCES plans (id) ON DELETE RESTRICT,
    type           text NOT NULL,
    version        text NOT NULL,
    settings       jsonb NOT NULL DEFAULT '{}',
    -- deleting: k8s cleanup is pending, the reconciler removes the row when done.
    desired_state  text NOT NULL CHECK (desired_state IN ('running', 'stopped', 'deleting')),
    keep_data      boolean NOT NULL DEFAULT false,
    observed_state text NOT NULL DEFAULT 'creating',
    status_message text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX servers_owner_id_idx ON servers (owner_id);

CREATE TABLE audit_log (
    id         bigserial PRIMARY KEY,
    user_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    action     text NOT NULL,
    target     text NOT NULL DEFAULT '',
    details    jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_created_at_idx ON audit_log (created_at DESC);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE servers;
DROP TABLE user_quotas;
DROP TABLE default_quota;
DROP TABLE plans;
DROP TABLE sessions;
DROP TABLE users;
