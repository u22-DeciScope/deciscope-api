ALTER TABLE workspace_invitations
    ADD COLUMN IF NOT EXISTS token_hash TEXT;

ALTER TABLE workspace_invitations
    ADD COLUMN IF NOT EXISTS expires_at TEXT;

ALTER TABLE workspace_invitations
    ADD COLUMN IF NOT EXISTS revoked_at TEXT;

ALTER TABLE workspace_invitations
    ADD COLUMN IF NOT EXISTS revoked_by TEXT REFERENCES users(id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_invitations_token_hash
    ON workspace_invitations (token_hash)
    WHERE token_hash IS NOT NULL;
