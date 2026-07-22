DROP INDEX IF EXISTS idx_meeting_tree_audit_runs_unapplied;

ALTER TABLE meeting_tree_audit_runs
    DROP COLUMN IF EXISTS rejection_reasons,
    DROP COLUMN IF EXISTS operations_rejected,
    DROP COLUMN IF EXISTS operations_applied,
    DROP COLUMN IF EXISTS operations_valid,
    DROP COLUMN IF EXISTS operations_canonicalized,
    DROP COLUMN IF EXISTS operations_proposed,
    DROP COLUMN IF EXISTS consecutive_unapplied_runs,
    DROP COLUMN IF EXISTS result_classification;
