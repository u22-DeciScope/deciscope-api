ALTER TABLE user_sessions
    ADD COLUMN IF NOT EXISTS login_method TEXT NOT NULL DEFAULT 'password';
