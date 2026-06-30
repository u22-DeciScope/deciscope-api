DROP INDEX IF EXISTS idx_meeting_sessions_last_bot_status_at;
DROP INDEX IF EXISTS idx_meeting_sessions_thread_id;
DROP INDEX IF EXISTS idx_meeting_sessions_join_meeting_id;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS last_bot_status_at;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS end_reason;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS title_source;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS title;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS scheduled_end_at;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS scheduled_start_at;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS organizer_email;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS organizer_name;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS thread_id;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS organizer_id;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS external_meeting_id;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS canonical_join_web_url;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS join_web_url;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS join_meeting_id;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS provider;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS graph_title;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS user_provided_title;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS title_resolved_at;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS title_resolution_error_message;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS title_resolution_error_code;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS title_updated_at;
