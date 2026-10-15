CREATE TABLE posts (
 id text PRIMARY KEY,
 title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
 markdown text NOT NULL CHECK (octet_length(markdown) <= 262144),
 published_title text NOT NULL DEFAULT '',
 published_markdown text NOT NULL DEFAULT '',
 published boolean NOT NULL DEFAULT false,
 published_at timestamptz,
 public_updated_at timestamptz,
 updated_at timestamptz NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 CHECK (NOT published OR (published_at IS NOT NULL AND public_updated_at IS NOT NULL))
);
CREATE INDEX posts_public_order ON posts (published_at DESC, id) WHERE published;
CREATE TABLE sessions (
 token_hash text PRIMARY KEY,
 expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_expiry ON sessions (expires_at);
CREATE TABLE login_attempts (
 window_start bigint PRIMARY KEY,
 attempts integer NOT NULL
);
