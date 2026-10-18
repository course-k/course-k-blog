CREATE TABLE creation_requests (
 request_key text PRIMARY KEY,
 payload_hash text NOT NULL,
 post_id text NOT NULL UNIQUE REFERENCES posts(id) ON DELETE CASCADE
);
