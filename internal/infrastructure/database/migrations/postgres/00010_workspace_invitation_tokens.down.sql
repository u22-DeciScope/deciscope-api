DROP INDEX IF EXISTS idx_workspace_invitations_token_hash;

ALTER TABLE workspace_invitations
    DROP COLUMN IF EXISTS revoked_by;

ALTER TABLE workspace_invitations
    DROP COLUMN IF EXISTS revoked_at;

ALTER TABLE workspace_invitations
    DROP COLUMN IF EXISTS expires_at;

ALTER TABLE workspace_invitations
    DROP COLUMN IF EXISTS token_hash;
