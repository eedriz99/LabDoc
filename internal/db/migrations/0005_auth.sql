-- Operator accounts (AdGuard Home style: no self-service public signup, no
-- roles). The first account is created via the one-time /setup page, which
-- refuses to run once any row exists here; further accounts, if wanted, are
-- added from the authenticated /account page.
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Server-side session store; the cookie holds only the opaque token.
CREATE TABLE sessions (
    token      TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    expires_at TEXT NOT NULL
);

CREATE INDEX sessions_expires ON sessions (expires_at);
