DROP INDEX IF EXISTS idx_transcript_segments_session_order;
ALTER TABLE transcript_segments DROP COLUMN IF EXISTS session_id;
DROP TABLE IF EXISTS meeting_sessions;
