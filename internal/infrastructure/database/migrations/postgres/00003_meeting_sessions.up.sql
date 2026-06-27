CREATE TABLE IF NOT EXISTS meeting_sessions (
    id TEXT PRIMARY KEY,
    join_url TEXT NOT NULL,
    join_url_hash TEXT NOT NULL,
    status TEXT NOT NULL,
    bot_call_id TEXT,
    requested_at TEXT NOT NULL,
    command_sent_at TEXT,
    joined_at TEXT,
    ended_at TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_meeting_sessions_status
    ON meeting_sessions (status);

CREATE INDEX IF NOT EXISTS idx_meeting_sessions_join_url_hash
    ON meeting_sessions (join_url_hash);

CREATE INDEX IF NOT EXISTS idx_meeting_sessions_bot_call_id
    ON meeting_sessions (bot_call_id);

ALTER TABLE transcript_segments
    ADD COLUMN IF NOT EXISTS session_id TEXT;

CREATE INDEX IF NOT EXISTS idx_transcript_segments_session_order
    ON transcript_segments (session_id, sequence_no);
