-- Migration tracking. Up-only, numbered.
CREATE TABLE schema_migrations (
  version    INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

-- Single-row settings: persisted secrets generated on first run.
CREATE TABLE settings (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  session_key BLOB,
  data_key    BLOB,
  created_at  TEXT NOT NULL
);

-- Users
CREATE TABLE users (
  id              INTEGER PRIMARY KEY,
  oidc_sub        TEXT NOT NULL UNIQUE,
  email           TEXT NOT NULL,
  display_name    TEXT NOT NULL,
  is_admin        INTEGER NOT NULL DEFAULT 0,
  display_tz      TEXT NOT NULL DEFAULT 'UTC',
  created_at      TEXT NOT NULL,
  last_login_at   TEXT
);
CREATE UNIQUE INDEX users_email_lower ON users (LOWER(email));

-- App passwords for CalDAV clients
CREATE TABLE app_passwords (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  label           TEXT NOT NULL,
  password_hash   TEXT NOT NULL,
  created_at      TEXT NOT NULL,
  last_used_at    TEXT,
  last_used_ip    TEXT,
  revoked_at      TEXT
);

-- Calendars. Two source types in v1: 'native' and 'ics'.
CREATE TABLE calendars (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug            TEXT NOT NULL,
  source_type     TEXT NOT NULL CHECK (source_type IN ('native','ics')),
  display_name    TEXT NOT NULL,
  color           TEXT,
  description     TEXT,
  ctag            TEXT NOT NULL,
  sync_seq        INTEGER NOT NULL DEFAULT 0,
  supports_vtodo  INTEGER NOT NULL DEFAULT 1,
  created_at      TEXT NOT NULL,
  -- ICS source fields, NULL when source_type='native'
  ics_url             TEXT,
  ics_poll_interval   INTEGER,
  ics_etag            TEXT,
  ics_last_modified   TEXT,
  ics_last_polled_at  TEXT,
  ics_last_status     TEXT,
  ics_last_error      TEXT,
  ics_basic_user      TEXT,
  ics_basic_pass_enc  BLOB
);
CREATE INDEX calendars_user ON calendars (user_id);
CREATE UNIQUE INDEX calendars_user_slug ON calendars (user_id, slug);

-- One row per stored iCalendar object.
CREATE TABLE objects (
  id              INTEGER PRIMARY KEY,
  calendar_id     INTEGER NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
  uid             TEXT NOT NULL,
  href            TEXT NOT NULL,
  etag            TEXT NOT NULL,
  ical_blob       BLOB NOT NULL,
  size_bytes      INTEGER NOT NULL,
  component_type  TEXT NOT NULL CHECK (component_type IN ('VEVENT','VTODO')),
  first_occurrence_utc TEXT,
  last_occurrence_utc  TEXT,
  has_rrule       INTEGER NOT NULL DEFAULT 0,
  has_scheduling  INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL,
  modified_at     TEXT NOT NULL,
  UNIQUE (calendar_id, href),
  UNIQUE (calendar_id, uid)
);
CREATE INDEX objects_first ON objects (calendar_id, first_occurrence_utc);
CREATE INDEX objects_last  ON objects (calendar_id, last_occurrence_utc);

-- Per-collection change journal that powers sync-collection.
CREATE TABLE object_changes (
  id              INTEGER PRIMARY KEY,
  calendar_id     INTEGER NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
  seq             INTEGER NOT NULL,
  op              TEXT NOT NULL CHECK (op IN ('added','modified','deleted')),
  href            TEXT NOT NULL,
  etag            TEXT,
  changed_at      TEXT NOT NULL
);
CREATE INDEX object_changes_lookup ON object_changes (calendar_id, seq);

-- WebDAV ACL: grant another local user access to one of your calendars.
CREATE TABLE calendar_acl (
  calendar_id     INTEGER NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
  grantee_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  privilege       TEXT NOT NULL CHECK (privilege IN ('read','read-write')),
  granted_at      TEXT NOT NULL,
  PRIMARY KEY (calendar_id, grantee_user_id)
);

-- Per-attendee state for each scheduled object.
CREATE TABLE attendee_state (
  id              INTEGER PRIMARY KEY,
  object_id       INTEGER NOT NULL REFERENCES objects(id) ON DELETE CASCADE,
  attendee_email  TEXT NOT NULL,
  cn              TEXT,
  role            TEXT,
  partstat        TEXT NOT NULL DEFAULT 'NEEDS-ACTION',
  rsvp            INTEGER NOT NULL DEFAULT 1,
  is_local_user   INTEGER NOT NULL DEFAULT 0,
  local_user_id   INTEGER REFERENCES users(id),
  schedule_status TEXT,
  last_updated_at TEXT NOT NULL,
  UNIQUE (object_id, attendee_email)
);
CREATE INDEX attendee_state_email ON attendee_state (attendee_email);

-- Scheduling Inbox objects.
CREATE TABLE schedule_inbox_objects (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  href            TEXT NOT NULL,
  etag            TEXT NOT NULL,
  ical_blob       BLOB NOT NULL,
  itip_method     TEXT NOT NULL CHECK (itip_method IN ('REQUEST','REPLY','CANCEL','COUNTER','REFRESH')),
  uid             TEXT NOT NULL,
  origin_user_id  INTEGER REFERENCES users(id),
  origin_email    TEXT,
  created_at      TEXT NOT NULL,
  UNIQUE (user_id, href)
);
CREATE INDEX schedule_inbox_user ON schedule_inbox_objects (user_id);

-- Outbound iMIP queue.
CREATE TABLE imip_outbound_queue (
  id              INTEGER PRIMARY KEY,
  object_id       INTEGER REFERENCES objects(id) ON DELETE SET NULL,
  from_user_id    INTEGER NOT NULL REFERENCES users(id),
  to_address      TEXT NOT NULL,
  itip_method     TEXT NOT NULL,
  message_id      TEXT NOT NULL UNIQUE,
  in_reply_to     TEXT,
  uid             TEXT NOT NULL,
  body_blob       BLOB NOT NULL,
  status          TEXT NOT NULL CHECK (status IN ('pending','sending','sent','failed_retry','failed_final')),
  attempt_count   INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT,
  last_error      TEXT,
  created_at      TEXT NOT NULL,
  sent_at         TEXT
);
CREATE INDEX imip_outbound_pending ON imip_outbound_queue (status, next_attempt_at);

-- Inbound dedup.
CREATE TABLE imip_inbound_processed (
  message_id      TEXT PRIMARY KEY,
  first_seen_at   TEXT NOT NULL,
  uid             TEXT,
  outcome         TEXT NOT NULL CHECK (outcome IN ('applied','no_match','malformed','duplicate'))
);

-- Views: a named privacy configuration over a set of the user's calendars.
CREATE TABLE views (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  preset          TEXT NOT NULL CHECK (preset IN ('full','titles','busy')),
  busy_label      TEXT NOT NULL DEFAULT 'Busy',
  include_private INTEGER NOT NULL DEFAULT 0,
  include_cancelled INTEGER NOT NULL DEFAULT 0,
  include_tentative INTEGER NOT NULL DEFAULT 1,
  include_transparent INTEGER NOT NULL DEFAULT 0,
  fields_json     TEXT NOT NULL,
  created_at      TEXT NOT NULL,
  modified_at     TEXT NOT NULL
);
CREATE INDEX views_user ON views (user_id);

CREATE TABLE view_calendars (
  id              INTEGER PRIMARY KEY,
  view_id         INTEGER NOT NULL REFERENCES views(id) ON DELETE CASCADE,
  calendar_id     INTEGER NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
  override_preset TEXT CHECK (override_preset IN ('full','titles','busy')),
  fields_json     TEXT,
  UNIQUE (view_id, calendar_id)
);

CREATE TABLE share_tokens (
  id                INTEGER PRIMARY KEY,
  view_id           INTEGER NOT NULL REFERENCES views(id) ON DELETE CASCADE,
  label             TEXT NOT NULL,
  token_hash        BLOB NOT NULL UNIQUE,
  expires_at        TEXT,
  password_hash     TEXT,
  created_at        TEXT NOT NULL,
  revoked_at        TEXT,
  last_accessed_at  TEXT,
  last_accessed_ip  TEXT,
  access_count      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX share_tokens_view ON share_tokens (view_id);

CREATE TABLE audit_events (
  id            INTEGER PRIMARY KEY,
  ts            TEXT NOT NULL,
  actor_user_id INTEGER REFERENCES users(id),
  actor_kind    TEXT NOT NULL,
  event         TEXT NOT NULL,
  target_kind   TEXT,
  target_id     INTEGER,
  metadata_json TEXT,
  client_ip     TEXT
);
CREATE INDEX audit_events_ts ON audit_events (ts);
