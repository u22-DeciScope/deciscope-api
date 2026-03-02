CREATE TABLE IF NOT EXISTS user_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_type TEXT NOT NULL,
    device_name TEXT NOT NULL,
    user_agent TEXT NULL,
    ip_address TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NULL,
    revoke_reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id
    ON user_sessions (user_id);

CREATE INDEX IF NOT EXISTS idx_user_sessions_active
    ON user_sessions (user_id, revoked_at);

CREATE INDEX IF NOT EXISTS idx_user_sessions_revoked_at
    ON user_sessions (revoked_at);

CREATE INDEX IF NOT EXISTS idx_user_sessions_last_seen_at
    ON user_sessions (last_seen_at);
