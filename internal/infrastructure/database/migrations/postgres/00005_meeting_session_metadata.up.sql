ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS title TEXT NOT NULL DEFAULT 'Teams会議';

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS title_source TEXT NOT NULL DEFAULT 'fallback';

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS title_updated_at TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS user_provided_title TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS graph_title TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS provider TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS external_meeting_id TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS join_meeting_id TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS join_web_url TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS canonical_join_web_url TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS thread_id TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS organizer_id TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS organizer_name TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS organizer_email TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS scheduled_start_at TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS scheduled_end_at TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS title_resolution_error_code TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS title_resolution_error_message TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS title_resolved_at TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS end_reason TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS last_bot_status_at TEXT;

UPDATE meeting_sessions
SET title = 'Teams会議'
WHERE COALESCE(TRIM(title), '') = '';

UPDATE meeting_sessions
SET title_source = 'fallback'
WHERE COALESCE(TRIM(title_source), '') = '';

UPDATE meeting_sessions
SET title_updated_at = updated_at
WHERE COALESCE(TRIM(title_updated_at), '') = '';

UPDATE meeting_sessions
SET provider = 'teams'
WHERE COALESCE(TRIM(provider), '') = '';

UPDATE meeting_sessions
SET user_provided_title = title
WHERE title_source = 'user_input'
    AND COALESCE(TRIM(user_provided_title), '') = '';

UPDATE meeting_sessions
SET graph_title = title
WHERE title_source LIKE 'graph_%'
    AND COALESCE(TRIM(graph_title), '') = '';

UPDATE meeting_sessions
SET join_web_url = join_url
WHERE COALESCE(TRIM(join_web_url), '') = '';

UPDATE meeting_sessions
SET canonical_join_web_url = join_url
WHERE COALESCE(TRIM(canonical_join_web_url), '') = '';

CREATE INDEX IF NOT EXISTS idx_meeting_sessions_last_bot_status_at
    ON meeting_sessions (last_bot_status_at);

CREATE INDEX IF NOT EXISTS idx_meeting_sessions_thread_id
    ON meeting_sessions (thread_id);

CREATE INDEX IF NOT EXISTS idx_meeting_sessions_join_meeting_id
    ON meeting_sessions (join_meeting_id);
