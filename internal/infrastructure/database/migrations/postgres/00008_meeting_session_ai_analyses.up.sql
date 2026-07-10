CREATE TABLE IF NOT EXISTS meeting_session_ai_analyses (
    session_id     TEXT NOT NULL,
    analysis_type  TEXT NOT NULL,
    status         TEXT NOT NULL,
    version        BIGINT NOT NULL DEFAULT 0,
    payload        JSONB,
    model          TEXT,
    segment_count  INTEGER NOT NULL DEFAULT 0,
    input_chars    INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, analysis_type)
);

CREATE INDEX IF NOT EXISTS idx_meeting_session_ai_analyses_updated_at
    ON meeting_session_ai_analyses (updated_at);
