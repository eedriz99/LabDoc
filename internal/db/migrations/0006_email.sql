-- Optional email per account, used only to receive a password-reset link.
-- NULL means no email on file (or a change that hasn't been sent/verified
-- yet); email_verified_at NULL means it hasn't been confirmed, and an
-- unverified email is never used to send a reset link.
ALTER TABLE users ADD COLUMN email TEXT;
ALTER TABLE users ADD COLUMN email_verified_at TEXT;
CREATE UNIQUE INDEX users_email ON users (email) WHERE email IS NOT NULL;

-- Single-use, time-limited tokens for both the "verify this email" and
-- "reset your password" links. A fresh send for a given (user_id, purpose)
-- replaces any previous one (see internal/web/email.go).
CREATE TABLE email_tokens (
    token      TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
    email      TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    expires_at TEXT NOT NULL
);

CREATE INDEX email_tokens_user ON email_tokens (user_id, purpose);
