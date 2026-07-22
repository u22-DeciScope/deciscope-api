ALTER TABLE meeting_tree_audit_runs
    ADD COLUMN IF NOT EXISTS result_classification TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS consecutive_unapplied_runs INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS operations_proposed INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS operations_canonicalized INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS operations_valid INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS operations_applied INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS operations_rejected INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS rejection_reasons JSONB;

CREATE INDEX IF NOT EXISTS idx_meeting_tree_audit_runs_unapplied
    ON meeting_tree_audit_runs (session_id, created_at DESC)
    WHERE result_classification IN ('findings_only', 'rejected');
