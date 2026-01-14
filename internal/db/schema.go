package db

const schema = `
-- Package versions stored in the global store
CREATE TABLE IF NOT EXISTS packages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    source_path TEXT NOT NULL,
    store_path TEXT NOT NULL,
    files_count INTEGER NOT NULL DEFAULT 0,
    total_size INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(name, content_hash)
);

CREATE INDEX IF NOT EXISTS idx_packages_name ON packages(name);
CREATE INDEX IF NOT EXISTS idx_packages_hash ON packages(content_hash);

-- Projects that consume packages
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL UNIQUE,
    name TEXT,
    package_manager TEXT CHECK(package_manager IN ('npm', 'yarn', 'pnpm', 'bun')),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_projects_path ON projects(path);

-- Links between packages and projects
CREATE TABLE IF NOT EXISTS links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    package_id INTEGER NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    link_type TEXT NOT NULL DEFAULT 'hardlink' CHECK(link_type IN ('hardlink', 'copy')),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(package_id, project_id)
);

CREATE INDEX IF NOT EXISTS idx_links_package ON links(package_id);
CREATE INDEX IF NOT EXISTS idx_links_project ON links(project_id);

-- Active watch sessions
CREATE TABLE IF NOT EXISTS watches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    package_id INTEGER NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    pid INTEGER,
    started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(package_id)
);

-- File manifest for incremental updates
CREATE TABLE IF NOT EXISTS files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    package_id INTEGER NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    relative_path TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    size INTEGER NOT NULL,
    mode INTEGER NOT NULL,
    UNIQUE(package_id, relative_path)
);

CREATE INDEX IF NOT EXISTS idx_files_package ON files(package_id);

-- Tags for packages
CREATE TABLE IF NOT EXISTS tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    package_id INTEGER NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(package_id, name)
);

CREATE INDEX IF NOT EXISTS idx_tags_name ON tags(name);
`
