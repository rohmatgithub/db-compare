CREATE TABLE connections (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    engine         TEXT NOT NULL CHECK (engine IN ('mysql', 'postgres')),
    host           TEXT NOT NULL,
    port           INTEGER NOT NULL,
    database_name  TEXT NOT NULL,
    schema_name    TEXT NOT NULL DEFAULT '',
    username       TEXT NOT NULL,
    password_enc   BYTEA,
    ssl_mode       TEXT NOT NULL DEFAULT 'disable',
    server_version TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE compare_projects (
    id                   BIGSERIAL PRIMARY KEY,
    name                 TEXT NOT NULL UNIQUE,
    source_connection_id BIGINT NOT NULL REFERENCES connections (id),
    target_connection_id BIGINT NOT NULL REFERENCES connections (id),
    options              JSONB NOT NULL DEFAULT '{}',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE compare_runs (
    id           BIGSERIAL PRIMARY KEY,
    project_id   BIGINT NOT NULL REFERENCES compare_projects (id) ON DELETE CASCADE,
    status       TEXT NOT NULL,
    options      JSONB NOT NULL,
    progress     JSONB NOT NULL DEFAULT '{}',
    source_label TEXT NOT NULL,
    target_label TEXT NOT NULL,
    error        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ
);

CREATE INDEX compare_runs_project_idx ON compare_runs (project_id, id DESC);
CREATE INDEX compare_runs_status_idx ON compare_runs (status);

CREATE TABLE schema_diff_items (
    id          BIGSERIAL PRIMARY KEY,
    run_id      BIGINT NOT NULL REFERENCES compare_runs (id) ON DELETE CASCADE,
    object_type TEXT NOT NULL,
    object_name TEXT NOT NULL,
    status      TEXT NOT NULL,
    changes     JSONB NOT NULL DEFAULT '[]',
    source_ddl  TEXT NOT NULL DEFAULT '',
    target_ddl  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX schema_diff_items_run_idx ON schema_diff_items (run_id, id);

CREATE TABLE table_results (
    run_id            BIGINT NOT NULL REFERENCES compare_runs (id) ON DELETE CASCADE,
    table_name        TEXT NOT NULL,
    key_columns       JSONB NOT NULL DEFAULT '[]',
    columns           JSONB NOT NULL DEFAULT '[]',
    notes             JSONB NOT NULL DEFAULT '[]',
    source_rows       BIGINT,
    target_rows       BIGINT,
    data_status       TEXT NOT NULL,
    rowdiff_status    TEXT NOT NULL,
    identical_count   BIGINT NOT NULL DEFAULT 0,
    different_count   BIGINT NOT NULL DEFAULT 0,
    only_source_count BIGINT NOT NULL DEFAULT 0,
    only_target_count BIGINT NOT NULL DEFAULT 0,
    truncated         BOOLEAN NOT NULL DEFAULT false,
    error             TEXT NOT NULL DEFAULT '',
    duration_ms       BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, table_name)
);

CREATE TABLE row_diffs (
    id            BIGSERIAL PRIMARY KEY,
    run_id        BIGINT NOT NULL REFERENCES compare_runs (id) ON DELETE CASCADE,
    table_name    TEXT NOT NULL,
    category      TEXT NOT NULL,
    key_values    JSONB NOT NULL,
    source_values JSONB,
    target_values JSONB,
    diff_columns  JSONB NOT NULL DEFAULT '[]'
);

CREATE INDEX row_diffs_lookup_idx ON row_diffs (run_id, table_name, category, id);
CREATE INDEX row_diffs_table_idx ON row_diffs (run_id, table_name, id);

CREATE TABLE audit_logs (
    id         BIGSERIAL PRIMARY KEY,
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL,
    target     TEXT NOT NULL,
    details    JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_logs_created_idx ON audit_logs (created_at DESC);
