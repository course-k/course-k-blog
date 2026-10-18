-- MySQL DDL commits implicitly. Statements must be safe to rerun if a process
-- stops before schema_migrations records completion. Runtime users have DML only.
CREATE TABLE IF NOT EXISTS posts (
    id varchar(100) PRIMARY KEY,
    title varchar(200) NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    markdown mediumtext NOT NULL CHECK (octet_length(markdown) <= 262144),
    published_title varchar(200) NOT NULL DEFAULT '',
    published_markdown mediumtext NOT NULL DEFAULT (''),
    published boolean NOT NULL DEFAULT false,
    published_at datetime(6),
    public_updated_at datetime(6),
    updated_at datetime(6) NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    CHECK (NOT published OR (published_at IS NOT NULL AND public_updated_at IS NOT NULL)),
    INDEX posts_public_order (published,published_at DESC,id)
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
CREATE TABLE IF NOT EXISTS sessions (
    token_hash varchar(100) PRIMARY KEY,
    expires_at datetime(6) NOT NULL,
    INDEX sessions_expiry(expires_at)
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
CREATE TABLE IF NOT EXISTS login_attempts (
    window_start bigint PRIMARY KEY,
    attempts integer NOT NULL
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
CREATE TABLE IF NOT EXISTS external_posts (
    source varchar(16) NOT NULL CHECK (source IN ('qiita','note','wantedly')),
    source_id varchar(200) NOT NULL,
    title varchar(500) NOT NULL,
    url text NOT NULL,
    published_at datetime(6) NOT NULL,
    fetched_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    title_override varchar(500),
    date_override datetime(6),
    hidden boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1,
    effective_date datetime(6) GENERATED ALWAYS AS (COALESCE(date_override,published_at)) VIRTUAL,
    PRIMARY KEY(source,source_id),
    INDEX external_posts_public_order(hidden,effective_date DESC,source,source_id)
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
CREATE TABLE IF NOT EXISTS sync_sources (
    source varchar(16) PRIMARY KEY CHECK (source IN ('qiita','note','wantedly')),
    status varchar(24) NOT NULL DEFAULT 'waiting',
    coverage varchar(24) NOT NULL DEFAULT 'unknown',
    imported_count integer NOT NULL DEFAULT 0,
    error_code varchar(100) NOT NULL DEFAULT '',
    last_attempt datetime(6),
    last_success datetime(6),
    next_run datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    lease_until datetime(6),
    run_id varchar(100)
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
INSERT INTO sync_sources(source) VALUES ('qiita'),('note'),('wantedly') ON DUPLICATE KEY UPDATE source=source;
-- statement-break
CREATE TABLE IF NOT EXISTS images (
    id varchar(100) PRIMARY KEY,
    object_key varchar(255) NOT NULL UNIQUE,
    mime varchar(32) NOT NULL CHECK (mime IN ('image/png','image/jpeg')),
    byte_size integer NOT NULL CHECK (byte_size>0 AND byte_size<=5242880),
    sha256 varchar(64) NOT NULL,
    created_at datetime(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
CREATE TABLE IF NOT EXISTS post_images (
    post_id varchar(100) NOT NULL,
    image_id varchar(100) NOT NULL,
    snapshot varchar(16) NOT NULL CHECK (snapshot IN ('working','published')),
    PRIMARY KEY(post_id,image_id,snapshot),
    INDEX post_images_by_image(image_id,snapshot,post_id),
    FOREIGN KEY(post_id) REFERENCES posts(id) ON DELETE CASCADE,
    FOREIGN KEY(image_id) REFERENCES images(id)
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
CREATE TABLE IF NOT EXISTS operation_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton=1),
    generation bigint NOT NULL DEFAULT 0,
    backed_up_generation bigint NOT NULL DEFAULT 0,
    due_at datetime(6),
    lease_until datetime(6),
    lease_id varchar(100),
    last_attempt datetime(6),
    last_success datetime(6),
    last_error varchar(100) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
INSERT INTO operation_state(singleton) VALUES(true) ON DUPLICATE KEY UPDATE singleton=singleton;
-- statement-break
CREATE TABLE IF NOT EXISTS backups (
    id varchar(100) PRIMARY KEY,
    generation bigint NOT NULL,
    object_key varchar(255) NOT NULL UNIQUE,
    created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    byte_size bigint NOT NULL,
    sha256 varchar(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
CREATE TABLE IF NOT EXISTS login_flows (
    state_hash varchar(100) PRIMARY KEY,
    nonce varchar(100) NOT NULL,
    verifier varchar(100) NOT NULL,
    expires_at datetime(6) NOT NULL,
    INDEX login_flows_expiry(expires_at)
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
CREATE TABLE IF NOT EXISTS creation_requests (
    request_key varchar(100) PRIMARY KEY,
    payload_hash varchar(100) NOT NULL,
    post_id varchar(100) NOT NULL UNIQUE,
    FOREIGN KEY(post_id) REFERENCES posts(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
-- statement-break
CREATE TRIGGER IF NOT EXISTS posts_insert_changed
AFTER INSERT ON posts FOR EACH ROW
BEGIN
    DECLARE deadline datetime(6);
    SET deadline = IF(NEW.published, NOW(6), CURRENT_DATE + INTERVAL 1 DAY);
    UPDATE operation_state
    SET generation = generation + 1,
        due_at = LEAST(COALESCE(due_at, deadline), deadline)
    WHERE singleton;
END;
-- statement-break
CREATE TRIGGER IF NOT EXISTS posts_update_changed
AFTER UPDATE ON posts FOR EACH ROW
BEGIN
    DECLARE deadline datetime(6);
    SET deadline = IF(NEW.published AND NOT (
        OLD.published <=> NEW.published
        AND OLD.published_title <=> NEW.published_title
        AND OLD.published_markdown <=> NEW.published_markdown
        AND OLD.public_updated_at <=> NEW.public_updated_at
    ), NOW(6), CURRENT_DATE + INTERVAL 1 DAY);
    IF NOT (
        OLD.title <=> NEW.title
        AND OLD.markdown <=> NEW.markdown
        AND OLD.published <=> NEW.published
        AND OLD.published_title <=> NEW.published_title
        AND OLD.published_markdown <=> NEW.published_markdown
        AND OLD.published_at <=> NEW.published_at
        AND OLD.public_updated_at <=> NEW.public_updated_at
    ) THEN
        UPDATE operation_state
        SET generation = generation + 1,
            due_at = LEAST(COALESCE(due_at, deadline), deadline)
        WHERE singleton;
    END IF;
END;
-- statement-break
CREATE TRIGGER IF NOT EXISTS images_insert_changed
AFTER INSERT ON images FOR EACH ROW
BEGIN
    DECLARE deadline datetime(6);
    SET deadline = CURRENT_DATE + INTERVAL 1 DAY;
    UPDATE operation_state
    SET generation = generation + 1,
        due_at = LEAST(COALESCE(due_at, deadline), deadline)
    WHERE singleton;
END;
-- statement-break
CREATE TRIGGER IF NOT EXISTS external_posts_insert_changed
AFTER INSERT ON external_posts FOR EACH ROW
BEGIN
    DECLARE deadline datetime(6);
    SET deadline = CURRENT_DATE + INTERVAL 1 DAY;
    UPDATE operation_state
    SET generation = generation + 1,
        due_at = LEAST(COALESCE(due_at, deadline), deadline)
    WHERE singleton;
END;
-- statement-break
CREATE TRIGGER IF NOT EXISTS external_posts_update_changed
AFTER UPDATE ON external_posts FOR EACH ROW
BEGIN
    DECLARE deadline datetime(6);
    SET deadline = CURRENT_DATE + INTERVAL 1 DAY;
    IF NOT (
        OLD.title <=> NEW.title
        AND OLD.url <=> NEW.url
        AND OLD.published_at <=> NEW.published_at
        AND OLD.title_override <=> NEW.title_override
        AND OLD.date_override <=> NEW.date_override
        AND OLD.hidden <=> NEW.hidden
    ) THEN
        UPDATE operation_state
        SET generation = generation + 1,
            due_at = LEAST(COALESCE(due_at, deadline), deadline)
        WHERE singleton;
    END IF;
END;
