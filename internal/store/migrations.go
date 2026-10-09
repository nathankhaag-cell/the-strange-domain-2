package store

// migrations are applied in order; the index+1 is stored in PRAGMA user_version.
// Never edit a migration that has shipped; append a new one.
var migrations = []string{
	`
CREATE TABLE users (
	id         TEXT PRIMARY KEY,
	callsign   TEXT NOT NULL UNIQUE COLLATE NOCASE,
	created_at INTEGER NOT NULL
);

-- Each device holds its own keys; the node stores only public keys.
CREATE TABLE devices (
	id          TEXT PRIMARY KEY,
	user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name        TEXT NOT NULL,
	identity_pk BLOB NOT NULL,
	created_at  INTEGER NOT NULL
);

CREATE TABLE domains (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	owner_id   TEXT NOT NULL REFERENCES users(id),
	created_at INTEGER NOT NULL
);

-- Role names are per-domain data so owners can rename them; the defaults
-- come from the theme Nathan approves.
CREATE TABLE roles (
	id          TEXT PRIMARY KEY,
	domain_id   TEXT NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
	name        TEXT NOT NULL,
	rank        INTEGER NOT NULL,
	permissions INTEGER NOT NULL,
	is_default  INTEGER NOT NULL DEFAULT 0,
	UNIQUE (domain_id, name)
);

CREATE TABLE members (
	domain_id TEXT NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
	user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role_id   TEXT NOT NULL REFERENCES roles(id),
	muted     INTEGER NOT NULL DEFAULT 0,
	joined_at INTEGER NOT NULL,
	PRIMARY KEY (domain_id, user_id)
);

CREATE TABLE bans (
	domain_id TEXT NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
	user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	banned_by TEXT NOT NULL REFERENCES users(id),
	reason    TEXT NOT NULL DEFAULT '',
	banned_at INTEGER NOT NULL,
	PRIMARY KEY (domain_id, user_id)
);

CREATE TABLE channels (
	id         TEXT PRIMARY KEY,
	domain_id  TEXT NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	kind       TEXT NOT NULL CHECK (kind IN ('text', 'voice')),
	position   INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	UNIQUE (domain_id, name)
);

CREATE TABLE invites (
	token      TEXT PRIMARY KEY,
	domain_id  TEXT NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
	created_by TEXT NOT NULL REFERENCES users(id),
	max_uses   INTEGER NOT NULL DEFAULT 0,
	uses       INTEGER NOT NULL DEFAULT 0,
	expires_at INTEGER NOT NULL DEFAULT 0
);

-- The audit log records moderation and settings changes per domain.
CREATE TABLE audit_log (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	domain_id TEXT NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
	actor_id  TEXT NOT NULL,
	action    TEXT NOT NULL,
	target    TEXT NOT NULL DEFAULT '',
	detail    TEXT NOT NULL DEFAULT '',
	at        INTEGER NOT NULL
);
CREATE INDEX audit_log_domain ON audit_log(domain_id, id);
`,
}
