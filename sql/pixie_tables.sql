-- image table
CREATE TABLE IF NOT EXISTS image (
    uuid CHAR(36) PRIMARY KEY,
    title VARCHAR(128) NOT NULL,
    description VARCHAR(512) NOT NULL,
    file_name VARCHAR(128) NOT NULL,
    file_type VARCHAR(32) NOT NULL,
    object_key VARCHAR(256) NOT NULL,
    slug VARCHAR(128) NOT NULL,
    slug_index VARCHAR(128) NOT NULL,
    width INT,
    height INT,
    size BIGINT,
    image_date VARCHAR(128),
    created_at TIMESTAMP NOT NULL DEFAULT UTC_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT UTC_TIMESTAMP,
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    is_published BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE UNIQUE INDEX IF NOT EXISTS image_slug_index_idx ON image (slug_index);

-- album table
CREATE TABLE IF NOT EXISTS album (
    uuid CHAR(36) PRIMARY KEY,
    title VARCHAR(128) NOT NULL,
    description VARCHAR(512) NOT NULL,
    slug VARCHAR(128) NOT NULL,
    slug_index VARCHAR(128) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT UTC_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT UTC_TIMESTAMP,
    is_archived BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE UNIQUE INDEX IF NOT EXISTS album_slug_index_idx ON album (slug_index);

-- permission table
CREATE TABLE permission (
    uuid CHAR(36) PRIMARY KEY,
    service VARCHAR(32) NOT NULL,
    name VARCHAR(128) NOT NULL,
    description VARCHAR(512) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT UTC_TIMESTAMP,
    active BOOLEAN NOT NULL,
    slug CHAR(128) NOT NULL
    slug_index CHAR(128) NOT NULL,
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_permission_slug_index ON permission (slug_index);


-- patron table, ie, users
CREATE TABLE IF NOT EXISTS patron (
    uuid CHAR(36) PRIMARY KEY,
    username VARCHAR(128) NOT NULL,
    user_index VARCHAR(128) NOT NULL,
    slug VARCHAR(128) NOT NULL,
    slug_index VARCHAR(128) NOT NULL, 
    created_at TIMESTAMP NOT NULL DEFAULT UTC_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT UTC_TIMESTAMP,
    is_archived BOOLEAN NOT NULL,
    is_active BOOLEAN NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS patron_username_idx ON patron (user_index);
CREATE UNIQUE INDEX IF NOT EXISTS patron_slug_idx ON patron (slug_index);

-- album_image xref table
CREATE TABLE IF NOT EXISTS album_image (
    id INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    album_uuid CHAR(36) NOT NULL,
    image_uuid CHAR(36) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT UTC_TIMESTAMP,
    CONSTRAINT  fk_album_image_uuid FOREIGN KEY (album_uuid) REFERENCES album(uuid),
    CONSTRAINT fk_image_album_uuid FOREIGN KEY (image_uuid) REFERENCES image(uuid)
);
CREATE INDEX IF NOT EXISTS idx_album_image ON album_image (album_uuid);
CREATE INDEX IF NOT EXISTS idx_image_album ON album_image (image_uuid);


-- image_permission xref table
CREATE TABLE IF NOT EXISTS image_permission (
    id INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    image_uuid CHAR(36) NOT NULL,
    permission_uuid CHAR(36) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT UTC_TIMESTAMP,
    CONSTRAINT fk_image_permission_image_uuid FOREIGN KEY (image_uuid) REFERENCES image(uuid),
    CONSTRAINT fk_permission_image_uuid FOREIGN KEY (permission_uuid) REFERENCES permission(uuid)
);
CREATE INDEX IF NOT EXISTS idx_image_permission ON image_permission (image_uuid);
CREATE INDEX IF NOT EXISTS idx_permission_image ON image_permission (permission_uuid);

-- patron_permission xref table
CREATE TABLE IF NOT EXISTS patron_permission (
    id INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    patron_uuid CHAR(36) NOT NULL,
    permission_uuid CHAR(36) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT UTC_TIMESTAMP,
    CONSTRAINT fk_patron_permission_patron_uuid FOREIGN KEY (patron_uuid) REFERENCES patron(uuid),
    CONSTRAINT fk_permission_patron_uuid FOREIGN KEY (permission_uuid) REFERENCES permission(uuid)
);
CREATE INDEX IF NOT EXISTS idx_patron_permission ON patron_permission (patron_uuid);
CREATE INDEX IF NOT EXISTS idx_permission_patron ON patron_permission (permission_uuid);