package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	// SQLite driver - uncomment when modernc.org/sqlite is added to go.mod
	// _ "modernc.org/sqlite"
)

var (
	instance *DB
	once     sync.Once
)

// DB wraps the SQLite database connection
type DB struct {
	conn *sql.DB
	path string
}

// Package represents a published package
type Package struct {
	ID          int64
	Name        string
	Version     string
	ContentHash string
	SourcePath  string
	StorePath   string
	FilesCount  int
	TotalSize   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Project represents a project that consumes packages
type Project struct {
	ID             int64
	Path           string
	Name           string
	PackageManager string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Link represents a link between a package and a project
type Link struct {
	ID        int64
	PackageID int64
	ProjectID int64
	LinkType  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FileEntry represents a file in a package
type FileEntry struct {
	ID           int64
	PackageID    int64
	RelativePath string
	ContentHash  string
	Size         int64
	Mode         os.FileMode
}

// GetDB returns the singleton database instance
func GetDB() (*DB, error) {
	var initErr error
	once.Do(func() {
		instance, initErr = initDB()
	})
	if initErr != nil {
		return nil, initErr
	}
	return instance, nil
}

// initDB initializes the database
func initDB() (*DB, error) {
	storePath, err := getStorePath()
	if err != nil {
		return nil, err
	}

	dbPath := filepath.Join(storePath, "lnpm.db")

	// Ensure store directory exists
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable foreign keys
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Initialize schema
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return &DB{conn: conn, path: dbPath}, nil
}

// getStorePath returns the lnpm store path
func getStorePath() (string, error) {
	// Check LNPM_STORE environment variable first
	if storePath := os.Getenv("LNPM_STORE"); storePath != "" {
		return storePath, nil
	}

	// Default to ~/.lnpm
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(homeDir, ".lnpm"), nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// --- Package operations ---

// InsertPackage inserts a new package
func (db *DB) InsertPackage(pkg *Package) error {
	result, err := db.conn.Exec(`
		INSERT INTO packages (name, version, content_hash, source_path, store_path, files_count, total_size)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name, content_hash) DO UPDATE SET
			version = excluded.version,
			source_path = excluded.source_path,
			updated_at = CURRENT_TIMESTAMP
	`, pkg.Name, pkg.Version, pkg.ContentHash, pkg.SourcePath, pkg.StorePath, pkg.FilesCount, pkg.TotalSize)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	pkg.ID = id
	return nil
}

// GetPackageByName returns the latest package with the given name
func (db *DB) GetPackageByName(name string) (*Package, error) {
	row := db.conn.QueryRow(`
		SELECT id, name, version, content_hash, source_path, store_path, files_count, total_size, created_at, updated_at
		FROM packages
		WHERE name = ?
		ORDER BY updated_at DESC
		LIMIT 1
	`, name)

	var pkg Package
	err := row.Scan(&pkg.ID, &pkg.Name, &pkg.Version, &pkg.ContentHash, &pkg.SourcePath,
		&pkg.StorePath, &pkg.FilesCount, &pkg.TotalSize, &pkg.CreatedAt, &pkg.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pkg, nil
}

// GetPackageByHash returns a package by its content hash
func (db *DB) GetPackageByHash(name, hash string) (*Package, error) {
	row := db.conn.QueryRow(`
		SELECT id, name, version, content_hash, source_path, store_path, files_count, total_size, created_at, updated_at
		FROM packages
		WHERE name = ? AND content_hash = ?
	`, name, hash)

	var pkg Package
	err := row.Scan(&pkg.ID, &pkg.Name, &pkg.Version, &pkg.ContentHash, &pkg.SourcePath,
		&pkg.StorePath, &pkg.FilesCount, &pkg.TotalSize, &pkg.CreatedAt, &pkg.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pkg, nil
}

// ListPackages returns all packages in the store
func (db *DB) ListPackages() ([]*Package, error) {
	rows, err := db.conn.Query(`
		SELECT id, name, version, content_hash, source_path, store_path, files_count, total_size, created_at, updated_at
		FROM packages
		ORDER BY name, updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var packages []*Package
	for rows.Next() {
		var pkg Package
		err := rows.Scan(&pkg.ID, &pkg.Name, &pkg.Version, &pkg.ContentHash, &pkg.SourcePath,
			&pkg.StorePath, &pkg.FilesCount, &pkg.TotalSize, &pkg.CreatedAt, &pkg.UpdatedAt)
		if err != nil {
			return nil, err
		}
		packages = append(packages, &pkg)
	}
	return packages, rows.Err()
}

// DeletePackage deletes a package by ID
func (db *DB) DeletePackage(id int64) error {
	_, err := db.conn.Exec("DELETE FROM packages WHERE id = ?", id)
	return err
}

// --- Project operations ---

// InsertProject inserts or updates a project
func (db *DB) InsertProject(proj *Project) error {
	result, err := db.conn.Exec(`
		INSERT INTO projects (path, name, package_manager)
		VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			name = excluded.name,
			package_manager = excluded.package_manager,
			updated_at = CURRENT_TIMESTAMP
	`, proj.Path, proj.Name, proj.PackageManager)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	proj.ID = id
	return nil
}

// GetProjectByPath returns a project by its path
func (db *DB) GetProjectByPath(path string) (*Project, error) {
	row := db.conn.QueryRow(`
		SELECT id, path, name, package_manager, created_at, updated_at
		FROM projects
		WHERE path = ?
	`, path)

	var proj Project
	err := row.Scan(&proj.ID, &proj.Path, &proj.Name, &proj.PackageManager, &proj.CreatedAt, &proj.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &proj, nil
}

// --- Link operations ---

// InsertLink creates a link between a package and project
func (db *DB) InsertLink(link *Link) error {
	result, err := db.conn.Exec(`
		INSERT INTO links (package_id, project_id, link_type)
		VALUES (?, ?, ?)
		ON CONFLICT(package_id, project_id) DO UPDATE SET
			link_type = excluded.link_type,
			updated_at = CURRENT_TIMESTAMP
	`, link.PackageID, link.ProjectID, link.LinkType)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	link.ID = id
	return nil
}

// GetLinksForPackage returns all links for a package
func (db *DB) GetLinksForPackage(packageID int64) ([]*Link, error) {
	rows, err := db.conn.Query(`
		SELECT id, package_id, project_id, link_type, created_at, updated_at
		FROM links
		WHERE package_id = ?
	`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []*Link
	for rows.Next() {
		var link Link
		err := rows.Scan(&link.ID, &link.PackageID, &link.ProjectID, &link.LinkType, &link.CreatedAt, &link.UpdatedAt)
		if err != nil {
			return nil, err
		}
		links = append(links, &link)
	}
	return links, rows.Err()
}

// GetLinksForProject returns all links for a project
func (db *DB) GetLinksForProject(projectID int64) ([]*Link, error) {
	rows, err := db.conn.Query(`
		SELECT id, package_id, project_id, link_type, created_at, updated_at
		FROM links
		WHERE project_id = ?
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []*Link
	for rows.Next() {
		var link Link
		err := rows.Scan(&link.ID, &link.PackageID, &link.ProjectID, &link.LinkType, &link.CreatedAt, &link.UpdatedAt)
		if err != nil {
			return nil, err
		}
		links = append(links, &link)
	}
	return links, rows.Err()
}

// DeleteLink removes a link
func (db *DB) DeleteLink(packageID, projectID int64) error {
	_, err := db.conn.Exec("DELETE FROM links WHERE package_id = ? AND project_id = ?", packageID, projectID)
	return err
}

// GetProjectsForPackage returns all projects linked to a package
func (db *DB) GetProjectsForPackage(packageID int64) ([]*Project, error) {
	rows, err := db.conn.Query(`
		SELECT p.id, p.path, p.name, p.package_manager, p.created_at, p.updated_at
		FROM projects p
		JOIN links l ON l.project_id = p.id
		WHERE l.package_id = ?
	`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		var proj Project
		err := rows.Scan(&proj.ID, &proj.Path, &proj.Name, &proj.PackageManager, &proj.CreatedAt, &proj.UpdatedAt)
		if err != nil {
			return nil, err
		}
		projects = append(projects, &proj)
	}
	return projects, rows.Err()
}

// --- File operations ---

// InsertFiles inserts file entries for a package
func (db *DB) InsertFiles(packageID int64, files []*FileEntry) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete existing files for this package
	if _, err := tx.Exec("DELETE FROM files WHERE package_id = ?", packageID); err != nil {
		return err
	}

	// Insert new files
	stmt, err := tx.Prepare(`
		INSERT INTO files (package_id, relative_path, content_hash, size, mode)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range files {
		if _, err := stmt.Exec(packageID, f.RelativePath, f.ContentHash, f.Size, f.Mode); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetFilesForPackage returns all files for a package
func (db *DB) GetFilesForPackage(packageID int64) ([]*FileEntry, error) {
	rows, err := db.conn.Query(`
		SELECT id, package_id, relative_path, content_hash, size, mode
		FROM files
		WHERE package_id = ?
	`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*FileEntry
	for rows.Next() {
		var f FileEntry
		err := rows.Scan(&f.ID, &f.PackageID, &f.RelativePath, &f.ContentHash, &f.Size, &f.Mode)
		if err != nil {
			return nil, err
		}
		files = append(files, &f)
	}
	return files, rows.Err()
}
