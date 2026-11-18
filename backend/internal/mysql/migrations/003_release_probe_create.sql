-- Exercises the reviewed production release with real DDL. The app never reads
-- this table and 004 drops it. Backup tables are unchanged, so archives from
-- version 1 and 2 still restore.
CREATE TABLE IF NOT EXISTS release_probe (id tinyint PRIMARY KEY)
