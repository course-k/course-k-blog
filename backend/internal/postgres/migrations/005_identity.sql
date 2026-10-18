CREATE TABLE login_flows (
 state_hash text PRIMARY KEY,
 nonce text NOT NULL,
 verifier text NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE INDEX login_flows_expiry ON login_flows(expires_at);
