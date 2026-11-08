-- Forward migration for autosave (untitled drafts), 10,000,000-byte images,
-- direct image uploads and manual-only external sync.
-- MySQL DDL commits implicitly, so every step checks information_schema and is
-- safe to rerun after a stop at any point. Unrelated CHECK constraints are left
-- untouched: legacy auto-named checks are matched by table and exact clause.
SET @step = (SELECT COALESCE((
    SELECT CONCAT('ALTER TABLE posts DROP CHECK `', cc.CONSTRAINT_NAME, '`')
    FROM information_schema.CHECK_CONSTRAINTS cc
    JOIN information_schema.TABLE_CONSTRAINTS tc
      ON tc.CONSTRAINT_SCHEMA = cc.CONSTRAINT_SCHEMA AND tc.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
    WHERE cc.CONSTRAINT_SCHEMA = DATABASE() AND tc.TABLE_NAME = 'posts' AND tc.CONSTRAINT_TYPE = 'CHECK'
      AND cc.CHECK_CLAUSE = '(char_length(`title`) between 1 and 200)'
    LIMIT 1), 'DO 0'))
-- statement-break
PREPARE migration_step FROM @step
-- statement-break
EXECUTE migration_step
-- statement-break
DEALLOCATE PREPARE migration_step
-- statement-break
SET @step = IF(EXISTS(
    SELECT 1 FROM information_schema.TABLE_CONSTRAINTS
    WHERE CONSTRAINT_SCHEMA = DATABASE() AND TABLE_NAME = 'posts' AND CONSTRAINT_NAME = 'posts_title_length'),
  'DO 0',
  'ALTER TABLE posts ADD CONSTRAINT posts_title_length CHECK (char_length(title) <= 200)')
-- statement-break
PREPARE migration_step FROM @step
-- statement-break
EXECUTE migration_step
-- statement-break
DEALLOCATE PREPARE migration_step
-- statement-break
SET @step = (SELECT COALESCE((
    SELECT CONCAT('ALTER TABLE images DROP CHECK `', cc.CONSTRAINT_NAME, '`')
    FROM information_schema.CHECK_CONSTRAINTS cc
    JOIN information_schema.TABLE_CONSTRAINTS tc
      ON tc.CONSTRAINT_SCHEMA = cc.CONSTRAINT_SCHEMA AND tc.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
    WHERE cc.CONSTRAINT_SCHEMA = DATABASE() AND tc.TABLE_NAME = 'images' AND tc.CONSTRAINT_TYPE = 'CHECK'
      AND cc.CHECK_CLAUSE = '((`byte_size` > 0) and (`byte_size` <= 5242880))'
    LIMIT 1), 'DO 0'))
-- statement-break
PREPARE migration_step FROM @step
-- statement-break
EXECUTE migration_step
-- statement-break
DEALLOCATE PREPARE migration_step
-- statement-break
SET @step = IF(EXISTS(
    SELECT 1 FROM information_schema.TABLE_CONSTRAINTS
    WHERE CONSTRAINT_SCHEMA = DATABASE() AND TABLE_NAME = 'images' AND CONSTRAINT_NAME = 'images_byte_size'),
  'DO 0',
  'ALTER TABLE images ADD CONSTRAINT images_byte_size CHECK (byte_size > 0 AND byte_size <= 10000000)')
-- statement-break
PREPARE migration_step FROM @step
-- statement-break
EXECUTE migration_step
-- statement-break
DEALLOCATE PREPARE migration_step
-- statement-break
-- Stop here, before recording the migration, if a legacy title or size check
-- survived under an unexpected clause text. PREPARE of this marker fails.
SET @step = IF(EXISTS(
    SELECT 1 FROM information_schema.CHECK_CONSTRAINTS cc
    JOIN information_schema.TABLE_CONSTRAINTS tc
      ON tc.CONSTRAINT_SCHEMA = cc.CONSTRAINT_SCHEMA AND tc.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
    WHERE cc.CONSTRAINT_SCHEMA = DATABASE() AND tc.CONSTRAINT_TYPE = 'CHECK'
      AND ((tc.TABLE_NAME = 'posts' AND cc.CHECK_CLAUSE LIKE '%`title`%' AND cc.CONSTRAINT_NAME <> 'posts_title_length')
        OR (tc.TABLE_NAME = 'images' AND cc.CHECK_CLAUSE LIKE '%`byte_size`%' AND cc.CONSTRAINT_NAME <> 'images_byte_size'))),
  'legacy title or image size check remains; inspect constraints before retrying',
  'DO 0')
-- statement-break
PREPARE migration_step FROM @step
-- statement-break
DEALLOCATE PREPARE migration_step
-- statement-break
SET @step = IF(EXISTS(
    SELECT 1 FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'sync_sources' AND COLUMN_NAME = 'requested'),
  'DO 0',
  'ALTER TABLE sync_sources ADD COLUMN requested boolean NOT NULL DEFAULT false')
-- statement-break
PREPARE migration_step FROM @step
-- statement-break
EXECUTE migration_step
-- statement-break
DEALLOCATE PREPARE migration_step
-- statement-break
ALTER TABLE sync_sources ALTER COLUMN status SET DEFAULT 'idle'
-- statement-break
-- Scheduled waits become idle; only an explicit owner request starts a sync.
UPDATE sync_sources SET status = 'idle' WHERE NOT requested AND status = 'waiting'
-- statement-break
CREATE TABLE IF NOT EXISTS image_uploads (
    upload_id varchar(100) PRIMARY KEY,
    client_image_id varchar(64) NOT NULL UNIQUE,
    file_name varchar(255) NOT NULL,
    declared_mime varchar(32) NOT NULL,
    declared_size integer NOT NULL,
    declared_sha256 char(64) NOT NULL,
    staging_key varchar(255) NOT NULL UNIQUE,
    status varchar(16) NOT NULL DEFAULT 'pending',
    image_id varchar(100) NOT NULL UNIQUE,
    image_key varchar(255),
    result_width integer,
    result_height integer,
    failure_code varchar(64) NOT NULL DEFAULT '',
    lease_id varchar(100),
    lease_until datetime(6),
    created_at datetime(6) NOT NULL,
    expires_at datetime(6) NOT NULL,
    completed_at datetime(6),
    CONSTRAINT image_uploads_mime CHECK (declared_mime IN ('image/png','image/jpeg','image/webp')),
    CONSTRAINT image_uploads_size CHECK (declared_size > 0 AND declared_size <= 10000000),
    CONSTRAINT image_uploads_status CHECK (status IN ('pending','processing','completed','failed','expired')),
    INDEX image_uploads_expiry(status, expires_at)
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin
