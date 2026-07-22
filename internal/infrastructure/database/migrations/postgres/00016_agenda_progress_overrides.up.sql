CREATE TABLE IF NOT EXISTS meeting_session_agenda_progress_overrides (
    session_id TEXT PRIMARY KEY,
    payload JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
