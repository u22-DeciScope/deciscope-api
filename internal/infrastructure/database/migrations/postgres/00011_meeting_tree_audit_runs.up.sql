CREATE TABLE IF NOT EXISTS meeting_tree_audit_runs (
    id                       TEXT PRIMARY KEY,
    session_id               TEXT NOT NULL REFERENCES meeting_sessions(id) ON DELETE CASCADE,
    based_on_tree_version    BIGINT NOT NULL,
    resulting_tree_version   BIGINT,
    mode                     TEXT NOT NULL,
    trigger_reason           TEXT NOT NULL,
    trigger_class            TEXT NOT NULL DEFAULT 'normal',
    task                     TEXT NOT NULL,
    deployment               TEXT NOT NULL DEFAULT '',
    model                    TEXT NOT NULL DEFAULT '',
    prompt_version           TEXT NOT NULL,
    snapshot_hash            TEXT NOT NULL,
    status                   TEXT NOT NULL,
    result                   TEXT NOT NULL,
    disposition              TEXT NOT NULL DEFAULT 'none',
    suppression_reason       TEXT NOT NULL DEFAULT '',
    provider_called          BOOLEAN NOT NULL DEFAULT FALSE,
    meeting_elapsed_seconds  BIGINT NOT NULL DEFAULT 0,
    input_summary            JSONB,
    input_payload            JSONB,
    raw_response             TEXT NOT NULL DEFAULT '',
    findings                 JSONB,
    operations               JSONB,
    validator_result         JSONB,
    prompt_tokens            INTEGER NOT NULL DEFAULT 0,
    completion_tokens        INTEGER NOT NULL DEFAULT 0,
    elapsed_ms               BIGINT NOT NULL DEFAULT 0,
    error_code               TEXT NOT NULL DEFAULT '',
    error_message            TEXT NOT NULL DEFAULT '',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at             TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_meeting_tree_audit_runs_session_created
    ON meeting_tree_audit_runs (session_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_meeting_tree_audit_runs_snapshot
    ON meeting_tree_audit_runs (session_id, based_on_tree_version, snapshot_hash);

CREATE UNIQUE INDEX IF NOT EXISTS idx_meeting_tree_audit_runs_active_claim
    ON meeting_tree_audit_runs (
        session_id, task, based_on_tree_version, snapshot_hash, prompt_version, deployment
    )
    WHERE status = 'running';

CREATE INDEX IF NOT EXISTS idx_meeting_tree_audit_runs_rate_limit
    ON meeting_tree_audit_runs (session_id, trigger_class, created_at DESC)
    WHERE provider_called AND task <> 'final_tree_review';
