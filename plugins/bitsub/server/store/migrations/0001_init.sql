CREATE TABLE meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE subscriptions (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	norm_name     TEXT NOT NULL,
	url           TEXT NOT NULL DEFAULT '',
	amount_minor  INTEGER NOT NULL,
	currency      TEXT NOT NULL,
	cycle         TEXT NOT NULL,
	interval      INTEGER NOT NULL,
	anchor_date   TEXT NOT NULL,
	trial_end     TEXT,
	intent        TEXT NOT NULL,
	status        TEXT NOT NULL,
	notice_days   INTEGER NOT NULL DEFAULT 0,
	access_until  TEXT,
	category      TEXT NOT NULL DEFAULT '',
	payment_label TEXT NOT NULL DEFAULT '',
	notes         TEXT NOT NULL DEFAULT '',
	reminder_days TEXT NOT NULL DEFAULT 'null',
	created_by    TEXT NOT NULL,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL,
	deleted_at    TEXT
);
CREATE INDEX subscriptions_norm_name ON subscriptions(norm_name);

CREATE TABLE charges (
	id              TEXT PRIMARY KEY,
	subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
	date            TEXT NOT NULL,
	amount_minor    INTEGER NOT NULL,
	currency        TEXT NOT NULL,
	aud_minor       INTEGER NOT NULL,
	fx_rate         REAL NOT NULL,
	fx_date         TEXT NOT NULL,
	kind            TEXT NOT NULL,
	UNIQUE (subscription_id, date, kind)
);
CREATE INDEX charges_date ON charges(date);

CREATE TABLE notifications (
	seq             INTEGER PRIMARY KEY AUTOINCREMENT,
	dedup_key       TEXT NOT NULL UNIQUE,
	subscription_id TEXT NOT NULL,
	summary         TEXT NOT NULL,
	body            TEXT NOT NULL,
	urgency         TEXT NOT NULL,
	actions         TEXT NOT NULL,
	issued_at       TEXT NOT NULL
);

CREATE TABLE calendar_links (
	subscription_id TEXT PRIMARY KEY,
	event_id        TEXT NOT NULL,
	hash            TEXT NOT NULL,
	synced_at       TEXT NOT NULL
);

CREATE TABLE audit (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	at              TEXT NOT NULL,
	actor           TEXT NOT NULL,
	action          TEXT NOT NULL,
	subscription_id TEXT NOT NULL,
	before          TEXT NOT NULL,
	after           TEXT NOT NULL
);
CREATE INDEX audit_subscription ON audit(subscription_id);
