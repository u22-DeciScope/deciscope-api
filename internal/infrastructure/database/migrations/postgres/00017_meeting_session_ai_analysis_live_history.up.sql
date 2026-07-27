CREATE TABLE IF NOT EXISTS meeting_session_ai_analysis_live_history (
    session_id  TEXT   NOT NULL,
    version     BIGINT NOT NULL,
    payload     JSONB,
    model       TEXT,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, version)
);
