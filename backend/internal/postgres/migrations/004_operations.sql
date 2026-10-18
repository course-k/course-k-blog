CREATE TABLE operation_state (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 generation bigint NOT NULL DEFAULT 0,
 backed_up_generation bigint NOT NULL DEFAULT 0,
 due_at timestamptz,
 lease_until timestamptz,
 lease_id text,
 last_attempt timestamptz,
 last_success timestamptz,
 last_error text NOT NULL DEFAULT ''
);
INSERT INTO operation_state(singleton,generation,due_at)
 SELECT true,CASE WHEN EXISTS(SELECT 1 FROM posts UNION ALL SELECT 1 FROM external_posts UNION ALL SELECT 1 FROM images) THEN 1 ELSE 0 END,now();
CREATE TABLE backups (
 id text PRIMARY KEY,
 generation bigint NOT NULL,
 object_key text NOT NULL UNIQUE,
 created_at timestamptz NOT NULL DEFAULT now(),
 byte_size bigint NOT NULL,
 sha256 text NOT NULL
);
CREATE FUNCTION note_content_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE deadline timestamptz;
BEGIN
 deadline := date_trunc('day',now())+interval '1 day';
 IF TG_TABLE_NAME='posts' THEN
  IF TG_OP='UPDATE' THEN
   IF (OLD.title,OLD.markdown,OLD.published,OLD.published_title,OLD.published_markdown,OLD.published_at,OLD.public_updated_at)
    IS NOT DISTINCT FROM (NEW.title,NEW.markdown,NEW.published,NEW.published_title,NEW.published_markdown,NEW.published_at,NEW.public_updated_at) THEN RETURN NULL; END IF;
   IF NEW.published AND (OLD.published,OLD.published_title,OLD.published_markdown,OLD.public_updated_at)
    IS DISTINCT FROM (NEW.published,NEW.published_title,NEW.published_markdown,NEW.public_updated_at) THEN deadline:=now(); END IF;
  ELSIF NEW.published THEN deadline:=now(); END IF;
 END IF;
 UPDATE operation_state SET generation=generation+1,due_at=LEAST(due_at,deadline) WHERE singleton;
 RETURN NULL;
END $$;
CREATE TRIGGER posts_changed AFTER INSERT OR UPDATE ON posts FOR EACH ROW EXECUTE FUNCTION note_content_change();
CREATE TRIGGER images_changed AFTER INSERT ON images FOR EACH ROW EXECUTE FUNCTION note_content_change();
CREATE TRIGGER external_added AFTER INSERT ON external_posts FOR EACH ROW EXECUTE FUNCTION note_content_change();
CREATE TRIGGER external_changed AFTER UPDATE ON external_posts FOR EACH ROW
 WHEN ((OLD.title,OLD.url,OLD.published_at,OLD.title_override,OLD.date_override,OLD.hidden) IS DISTINCT FROM (NEW.title,NEW.url,NEW.published_at,NEW.title_override,NEW.date_override,NEW.hidden))
 EXECUTE FUNCTION note_content_change();
