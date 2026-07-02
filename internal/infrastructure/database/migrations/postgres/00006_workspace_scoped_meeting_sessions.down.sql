DROP INDEX IF EXISTS idx_meeting_sessions_created_by_user;
DROP INDEX IF EXISTS idx_meeting_sessions_workspace_join_url_hash;
DROP INDEX IF EXISTS idx_meeting_sessions_workspace_updated;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS meeting_id;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS created_by_user_id;

ALTER TABLE meeting_sessions
    DROP COLUMN IF EXISTS workspace_id;

ALTER TABLE workspace_invitations
    DROP CONSTRAINT IF EXISTS workspace_invitations_role_check;

ALTER TABLE workspace_members
    DROP CONSTRAINT IF EXISTS workspace_members_role_check;

ALTER TABLE workspace_invitations
    ALTER COLUMN role SET DEFAULT 'member';

UPDATE workspace_invitations
SET role = 'member'
WHERE role = 'admin';

UPDATE workspace_members
SET role = 'member'
WHERE role = 'admin';
