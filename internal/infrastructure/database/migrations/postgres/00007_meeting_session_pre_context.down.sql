ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS custom_instruction;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS expected_output;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS concerns;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS decision_points;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS agenda;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS context;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS purpose;