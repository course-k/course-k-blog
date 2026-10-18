CREATE TABLE external_posts (
 source text NOT NULL CHECK (source IN ('qiita','note','wantedly')),
 source_id text NOT NULL,
 title text NOT NULL,
 url text NOT NULL,
 published_at timestamptz NOT NULL,
 fetched_at timestamptz NOT NULL DEFAULT now(),
 title_override text,
 date_override timestamptz,
 hidden boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1,
 PRIMARY KEY (source,source_id)
);
CREATE INDEX external_posts_public_order ON external_posts ((COALESCE(date_override,published_at)) DESC,source,source_id) WHERE NOT hidden;
CREATE TABLE sync_sources (
 source text PRIMARY KEY CHECK (source IN ('qiita','note','wantedly')),
 status text NOT NULL DEFAULT 'waiting',
 coverage text NOT NULL DEFAULT 'unknown',
 imported_count integer NOT NULL DEFAULT 0,
 error_code text NOT NULL DEFAULT '',
 last_attempt timestamptz,
 last_success timestamptz,
 next_run timestamptz NOT NULL DEFAULT now(),
 lease_until timestamptz,
 run_id text
);
INSERT INTO sync_sources (source) VALUES ('qiita'),('note'),('wantedly');
