PRAGMA foreign_keys = OFF;

ALTER TABLE meetings ADD COLUMN workspace_id TEXT;
ALTER TABLE jobs ADD COLUMN workspace_id TEXT;
ALTER TABLE uploads ADD COLUMN workspace_id TEXT;

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS user_identities (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    provider_subject TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(provider, provider_subject),
    FOREIGN KEY(user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS user_emails (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    email TEXT NOT NULL,
    normalized_email TEXT NOT NULL,
    verified INTEGER NOT NULL DEFAULT 1,
    is_primary INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    UNIQUE(normalized_email),
    FOREIGN KEY(user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY(created_by) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS workspace_members (
    workspace_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    role TEXT NOT NULL,
    joined_at TEXT NOT NULL,
    PRIMARY KEY(workspace_id, user_id),
    FOREIGN KEY(workspace_id) REFERENCES workspaces(id),
    FOREIGN KEY(user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS workspace_invitations (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    email TEXT NOT NULL,
    normalized_email TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'member',
    status TEXT NOT NULL DEFAULT 'pending',
    invited_by TEXT NOT NULL,
    accepted_by TEXT,
    created_at TEXT NOT NULL,
    accepted_at TEXT,
    FOREIGN KEY(workspace_id) REFERENCES workspaces(id),
    FOREIGN KEY(invited_by) REFERENCES users(id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_invitation_pending
ON workspace_invitations(workspace_id, normalized_email)
WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS user_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    current_workspace_id TEXT,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    revoked_at TEXT,
    FOREIGN KEY(user_id) REFERENCES users(id),
    FOREIGN KEY(current_workspace_id) REFERENCES workspaces(id)
);

CREATE INDEX IF NOT EXISTS idx_meetings_workspace ON meetings(workspace_id);
CREATE INDEX IF NOT EXISTS idx_jobs_workspace ON jobs(workspace_id);
CREATE INDEX IF NOT EXISTS idx_uploads_workspace ON uploads(workspace_id);
CREATE INDEX IF NOT EXISTS idx_workspace_members_user ON workspace_members(user_id);
CREATE INDEX IF NOT EXISTS idx_user_sessions_hash ON user_sessions(token_hash);

PRAGMA foreign_keys = ON;
