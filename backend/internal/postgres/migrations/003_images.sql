CREATE TABLE images (
 id text PRIMARY KEY,
 object_key text NOT NULL UNIQUE,
 mime text NOT NULL CHECK (mime IN ('image/png','image/jpeg')),
 byte_size integer NOT NULL CHECK (byte_size>0 AND byte_size<=5242880),
 sha256 text NOT NULL,
 created_at timestamptz NOT NULL
);
CREATE TABLE post_images (
 post_id text NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
 image_id text NOT NULL REFERENCES images(id),
 snapshot text NOT NULL CHECK (snapshot IN ('working','published')),
 PRIMARY KEY(post_id,image_id,snapshot)
);
CREATE INDEX post_images_by_image ON post_images(image_id,snapshot,post_id);
