CREATE TABLE IF NOT EXISTS users (
  id         text PRIMARY KEY,
  email      text UNIQUE NOT NULL,
  pass_hash  text NOT NULL,
  display    text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS prefs (
  user_id text PRIMARY KEY,
  theme   text NOT NULL DEFAULT 'amber',
  mode    text NOT NULL DEFAULT 'dark'
);

CREATE TABLE IF NOT EXISTS avatars (
  user_id      text PRIMARY KEY,
  content_type text NOT NULL,
  data         bytea NOT NULL
);

CREATE TABLE IF NOT EXISTS rooms (
  code       text PRIMARY KEY,
  host_id    text NOT NULL,
  title      text NOT NULL,
  kind       text NOT NULL,
  choices    text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Durable record of every vote, written by the OnEvent consumer in main.
-- The event-log offset is the primary key so redelivery (at-least-once,
-- multi-pod) is an idempotent no-op.
CREATE TABLE IF NOT EXISTS votes (
  offset_id  bigint PRIMARY KEY,
  room       text NOT NULL,
  choice     text NOT NULL,
  by_nick    text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
