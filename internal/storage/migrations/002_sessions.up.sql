-- Server-side web sessions. The cookie carries the signed id; everything else
-- lives here so sessions can be revoked and expiry can slide.
CREATE TABLE sessions (
  id           TEXT PRIMARY KEY,        -- random opaque id, also the cookie value (signed)
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  user_agent   TEXT,
  client_ip    TEXT
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expiry ON sessions (expires_at);

-- Short-lived OIDC login flow state (PKCE verifier + nonce), keyed by the
-- state parameter. Rows are deleted on callback or when they expire.
CREATE TABLE oidc_flows (
  state         TEXT PRIMARY KEY,
  code_verifier TEXT NOT NULL,
  nonce         TEXT NOT NULL,
  redirect_to   TEXT,
  created_at    TEXT NOT NULL,
  expires_at    TEXT NOT NULL
);
