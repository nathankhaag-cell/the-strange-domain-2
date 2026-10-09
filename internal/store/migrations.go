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
	`
-- The first account on a node becomes its administrator.
ALTER TABLE users ADD COLUMN is_node_admin INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX devices_identity_pk ON devices(identity_pk);

-- Single-use sign-in challenges, signed by the device's identity key.
CREATE TABLE auth_challenges (
	nonce      BLOB PRIMARY KEY,
	device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);

-- Sessions store only a SHA-256 hash of the bearer token.
CREATE TABLE sessions (
	token_hash TEXT PRIMARY KEY,
	device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
`,
	`
-- MLS delivery service. The node stores and orders opaque MLS messages; it
-- never holds keys and cannot read content.

-- Prepublished MLS KeyPackages, one consumed each time a device is added to a group.
CREATE TABLE key_packages (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	data       BLOB NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX key_packages_device ON key_packages(device_id, id);

-- Conclaves are DMs outside any domain: a Confession has two members, a
-- group Conclave more.
CREATE TABLE conclaves (
	id         TEXT PRIMARY KEY,
	created_by TEXT NOT NULL REFERENCES users(id),
	created_at INTEGER NOT NULL
);
CREATE TABLE conclave_members (
	conclave_id TEXT NOT NULL REFERENCES conclaves(id) ON DELETE CASCADE,
	user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	PRIMARY KEY (conclave_id, user_id)
);

-- One MLS group per channel or conclave; the group id is that row's id.
-- The node orders commits so every member agrees on the epoch.
CREATE TABLE mls_groups (
	id    TEXT PRIMARY KEY,
	epoch INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE mls_messages (
	seq           INTEGER PRIMARY KEY AUTOINCREMENT,
	group_id      TEXT NOT NULL REFERENCES mls_groups(id) ON DELETE CASCADE,
	sender_user   TEXT NOT NULL,
	sender_device TEXT NOT NULL,
	kind          TEXT NOT NULL CHECK (kind IN ('application', 'proposal', 'commit')),
	epoch         INTEGER NOT NULL,
	data          BLOB,
	deleted       INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL
);
CREATE INDEX mls_messages_group ON mls_messages(group_id, seq);

-- Welcome messages are addressed to one device and deleted once fetched.
CREATE TABLE mls_welcomes (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	group_id   TEXT NOT NULL REFERENCES mls_groups(id) ON DELETE CASCADE,
	data       BLOB NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX mls_welcomes_device ON mls_welcomes(device_id, id);
`,
	`
-- Several devices per account.

-- A signed-in device issues a short-lived, single-use code; a new device
-- presents it to register its own key under the same account.
CREATE TABLE device_link_codes (
	code_hash  TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);

-- Recovery: the client derives a key pair from a recovery code the person
-- writes down; the node keeps only the public key.
ALTER TABLE users ADD COLUMN recovery_pk BLOB;
CREATE TABLE recovery_challenges (
	nonce      BLOB PRIMARY KEY,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);

-- History transfer: an old device sends message history, encrypted on the
-- device for the new device's key, in chunks the new device takes once.
CREATE TABLE device_transfers (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	to_device   TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	from_device TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	transfer_id TEXT NOT NULL,
	chunk       INTEGER NOT NULL,
	total       INTEGER NOT NULL,
	data        BLOB NOT NULL,
	created_at  INTEGER NOT NULL,
	UNIQUE (transfer_id, chunk)
);
CREATE INDEX device_transfers_to ON device_transfers(to_device, id);
`,
	`
-- Attachments: files encrypted on the device with a fresh key that travels
-- only inside the MLS message. The node keeps the ciphertext on disk under
-- <data>/blobs and only this row here. A blob is uploaded for one group and
-- attached to one message (seq) when that message is sent; until then only
-- its uploader can fetch it.
CREATE TABLE blobs (
	id         TEXT PRIMARY KEY,
	owner_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	group_id   TEXT NOT NULL,
	size       INTEGER NOT NULL,
	seq        INTEGER,
	created_at INTEGER NOT NULL
);
CREATE INDEX blobs_owner ON blobs(owner_id);
CREATE INDEX blobs_message ON blobs(group_id, seq);

-- Profile pictures. Not end-to-end encrypted: like callsigns, the node can
-- see them. Small (a 256 px square), so they live in the database.
CREATE TABLE avatars (
	user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	mime    TEXT NOT NULL,
	data    BLOB NOT NULL,
	version INTEGER NOT NULL
);
`,
}
