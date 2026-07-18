UPDATE meeting_tree_audit_runs SET mode = 'apply_high_confidence' WHERE mode IS NULL;
ALTER TABLE meeting_tree_audit_runs ALTER COLUMN mode SET NOT NULL;
