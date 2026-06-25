CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS user_identities (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    provider TEXT NOT NULL,
    provider_subject TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(provider, provider_subject)
);

CREATE TABLE IF NOT EXISTS user_emails (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    email TEXT NOT NULL,
    normalized_email TEXT NOT NULL UNIQUE,
    verified BOOLEAN NOT NULL DEFAULT TRUE,
    is_primary BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workspace_members (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    user_id TEXT NOT NULL REFERENCES users(id),
    role TEXT NOT NULL,
    joined_at TEXT NOT NULL,
    PRIMARY KEY(workspace_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_workspace_members_user ON workspace_members(user_id);

CREATE TABLE IF NOT EXISTS workspace_invitations (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    email TEXT NOT NULL,
    normalized_email TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'member',
    status TEXT NOT NULL DEFAULT 'pending',
    invited_by TEXT NOT NULL REFERENCES users(id),
    accepted_by TEXT,
    created_at TEXT NOT NULL,
    accepted_at TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_invitation_pending
ON workspace_invitations(workspace_id, normalized_email)
WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS user_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    token_hash TEXT NOT NULL UNIQUE,
    current_workspace_id TEXT REFERENCES workspaces(id),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    revoked_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_user_sessions_hash ON user_sessions(token_hash);

CREATE TABLE IF NOT EXISTS meetings (
    id TEXT PRIMARY KEY,
    workspace_id TEXT,
    title TEXT NOT NULL,
    status TEXT NOT NULL,
    source TEXT NOT NULL,
    next_seq BIGINT NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    ended_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_meetings_workspace ON meetings(workspace_id);

CREATE TABLE IF NOT EXISTS meeting_events (
    id BIGSERIAL PRIMARY KEY,
    meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    type TEXT NOT NULL,
    ts_ms BIGINT NOT NULL,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(meeting_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_meeting_events_meeting_seq ON meeting_events(meeting_id, seq);

CREATE TABLE IF NOT EXISTS meeting_segments (
    id BIGSERIAL PRIMARY KEY,
    meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    segment_id TEXT NOT NULL,
    speaker_label TEXT NOT NULL,
    text TEXT NOT NULL,
    start_ms BIGINT NOT NULL DEFAULT 0,
    end_ms BIGINT NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    UNIQUE(meeting_id, segment_id)
);
CREATE INDEX IF NOT EXISTS idx_meeting_segments_meeting_seq ON meeting_segments(meeting_id, seq);

CREATE TABLE IF NOT EXISTS meeting_reports (
    id BIGSERIAL PRIMARY KEY,
    artifact_id TEXT NOT NULL UNIQUE,
    meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
    format TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meeting_reports_meeting ON meeting_reports(meeting_id);

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    meeting_id TEXT,
    result TEXT,
    error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_meeting ON jobs(meeting_id);
CREATE INDEX IF NOT EXISTS idx_jobs_workspace ON jobs(workspace_id);

CREATE TABLE IF NOT EXISTS uploads (
    id TEXT PRIMARY KEY,
    workspace_id TEXT,
    filename TEXT NOT NULL,
    media_type TEXT NOT NULL,
    path TEXT NOT NULL,
    job_id TEXT NOT NULL REFERENCES jobs(id),
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_uploads_workspace ON uploads(workspace_id);
