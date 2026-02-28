CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL,
    email TEXT NULL,
    display_name TEXT NULL,
    avatar_url TEXT NULL,
    deleted_at TIMESTAMPTZ NULL
);
