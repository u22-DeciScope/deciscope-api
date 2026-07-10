ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS purpose TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS context TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS agenda TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS decision_points TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS concerns TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS expected_output TEXT;

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS custom_instruction TEXT;