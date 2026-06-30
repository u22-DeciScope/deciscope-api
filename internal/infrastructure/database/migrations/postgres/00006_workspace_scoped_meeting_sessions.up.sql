UPDATE workspace_members
SET role = 'admin'
WHERE role = 'member';

UPDATE workspace_invitations
SET role = 'admin'
WHERE role = 'member';

ALTER TABLE workspace_invitations
    ALTER COLUMN role SET DEFAULT 'viewer';

ALTER TABLE workspace_members
    ADD CONSTRAINT workspace_members_role_check
    CHECK (role IN ('owner', 'admin', 'viewer'));

ALTER TABLE workspace_invitations
    ADD CONSTRAINT workspace_invitations_role_check
    CHECK (role IN ('admin', 'viewer'));

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS workspace_id TEXT REFERENCES workspaces(id);

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS created_by_user_id TEXT REFERENCES users(id);

ALTER TABLE meeting_sessions
    ADD COLUMN IF NOT EXISTS meeting_id TEXT REFERENCES meetings(id);

CREATE INDEX IF NOT EXISTS idx_meeting_sessions_workspace_updated
    ON meeting_sessions (workspace_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_meeting_sessions_workspace_join_url_hash
    ON meeting_sessions (workspace_id, join_url_hash);

CREATE INDEX IF NOT EXISTS idx_meeting_sessions_created_by_user
    ON meeting_sessions (created_by_user_id);
