package db

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	instance *DB
	once     sync.Once
)

// DB wraps the JSON file-based database
type DB struct {
	path string
	data *DBData
	mu   sync.RWMutex
}

// DBData represents the database structure
type DBData struct {
	Packages map[int64]*Package     `json:"packages"`
	Projects map[int64]*Project     `json:"projects"`
	Links    map[int64]*Link        `json:"links"`
	Files    map[int64][]*FileEntry `json:"files"`
	NextID   int64                  `json:"next_id"`
}

// Package represents a published package
type Package struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	ContentHash string    `json:"content_hash"`
	SourcePath  string    `json:"source_path"`
	StorePath   string    `json:"store_path"`
	FilesCount  int       `json:"files_count"`
	TotalSize   int64     `json:"total_size"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Project represents a project that consumes packages
type Project struct {
	ID             int64     `json:"id"`
	Path           string    `json:"path"`
	Name           string    `json:"name"`
	PackageManager string    `json:"package_manager"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Link represents a link between a package and a project
type Link struct {
	ID        int64     `json:"id"`
	PackageID int64     `json:"package_id"`
	ProjectID int64     `json:"project_id"`
	LinkType  string    `json:"link_type"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FileEntry represents a file in a package
type FileEntry struct {
	ID           int64       `json:"id"`
	PackageID    int64       `json:"package_id"`
	RelativePath string      `json:"relative_path"`
	ContentHash  string      `json:"content_hash"`
	Size         int64       `json:"size"`
	Mode         os.FileMode `json:"mode"`
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

	dbPath := filepath.Join(storePath, "lnpm.json")

	// Ensure store directory exists
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	db := &DB{
		path: dbPath,
		data: &DBData{
			Packages: make(map[int64]*Package),
			Projects: make(map[int64]*Project),
			Links:    make(map[int64]*Link),
			Files:    make(map[int64][]*FileEntry),
			NextID:   1,
		},
	}

	// Load existing data if file exists
	if data, err := os.ReadFile(dbPath); err == nil {
		if err := json.Unmarshal(data, db.data); err != nil {
			return nil, fmt.Errorf("failed to parse database: %w", err)
		}
	}

	// Ensure maps are initialized
	if db.data.Packages == nil {
		db.data.Packages = make(map[int64]*Package)
	}
	if db.data.Projects == nil {
		db.data.Projects = make(map[int64]*Project)
	}
	if db.data.Links == nil {
		db.data.Links = make(map[int64]*Link)
	}
	if db.data.Files == nil {
		db.data.Files = make(map[int64][]*FileEntry)
	}

	return db, nil
}

// getStorePath returns the lnpm store path
func getStorePath() (string, error) {
	if storePath := os.Getenv("LNPM_STORE"); storePath != "" {
		return storePath, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(homeDir, ".lnpm"), nil
}

// save persists the database to disk
func (db *DB) save() error {
	data, err := json.MarshalIndent(db.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(db.path, data, 0644)
}

// nextID returns the next available ID
func (db *DB) nextID() int64 {
	id := db.data.NextID
	db.data.NextID++
	return id
}

// Close saves and closes the database
func (db *DB) Close() error {
	return db.save()
}

// --- Package operations ---

// InsertPackage inserts or updates a package
func (db *DB) InsertPackage(pkg *Package) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Check if package with same name and hash exists
	for _, existing := range db.data.Packages {
		if existing.Name == pkg.Name && existing.ContentHash == pkg.ContentHash {
			// Update existing
			existing.Version = pkg.Version
			existing.SourcePath = pkg.SourcePath
			existing.StorePath = pkg.StorePath
			existing.FilesCount = pkg.FilesCount
			existing.TotalSize = pkg.TotalSize
			existing.UpdatedAt = time.Now()
			pkg.ID = existing.ID
			return db.save()
		}
	}

	// Insert new
	pkg.ID = db.nextID()
	pkg.CreatedAt = time.Now()
	pkg.UpdatedAt = time.Now()
	db.data.Packages[pkg.ID] = pkg

	return db.save()
}

// GetPackageByName returns the latest package with the given name
func (db *DB) GetPackageByName(name string) (*Package, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var latest *Package
	for _, pkg := range db.data.Packages {
		if pkg.Name == name {
			if latest == nil || pkg.UpdatedAt.After(latest.UpdatedAt) {
				latest = pkg
			}
		}
	}
	return latest, nil
}

// GetPackageByHash returns a package by its content hash
func (db *DB) GetPackageByHash(name, hash string) (*Package, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	for _, pkg := range db.data.Packages {
		if pkg.Name == name && pkg.ContentHash == hash {
			return pkg, nil
		}
	}
	return nil, nil
}

// ListPackages returns all packages in the store
func (db *DB) ListPackages() ([]*Package, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	packages := make([]*Package, 0, len(db.data.Packages))
	for _, pkg := range db.data.Packages {
		packages = append(packages, pkg)
	}
	return packages, nil
}

// DeletePackage deletes a package by ID
func (db *DB) DeletePackage(id int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	delete(db.data.Packages, id)
	delete(db.data.Files, id)
	return db.save()
}

// --- Project operations ---

// InsertProject inserts or updates a project
func (db *DB) InsertProject(proj *Project) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Check if project with same path exists
	for _, existing := range db.data.Projects {
		if existing.Path == proj.Path {
			existing.Name = proj.Name
			existing.PackageManager = proj.PackageManager
			existing.UpdatedAt = time.Now()
			proj.ID = existing.ID
			return db.save()
		}
	}

	// Insert new
	proj.ID = db.nextID()
	proj.CreatedAt = time.Now()
	proj.UpdatedAt = time.Now()
	db.data.Projects[proj.ID] = proj

	return db.save()
}

// GetProjectByPath returns a project by its path
func (db *DB) GetProjectByPath(path string) (*Project, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	for _, proj := range db.data.Projects {
		if proj.Path == path {
			return proj, nil
		}
	}
	return nil, nil
}

// --- Link operations ---

// InsertLink creates a link between a package and project
func (db *DB) InsertLink(link *Link) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Check if link exists
	for _, existing := range db.data.Links {
		if existing.PackageID == link.PackageID && existing.ProjectID == link.ProjectID {
			existing.LinkType = link.LinkType
			existing.UpdatedAt = time.Now()
			link.ID = existing.ID
			return db.save()
		}
	}

	// Insert new
	link.ID = db.nextID()
	link.CreatedAt = time.Now()
	link.UpdatedAt = time.Now()
	db.data.Links[link.ID] = link

	return db.save()
}

// GetLinksForPackage returns all links for a package
func (db *DB) GetLinksForPackage(packageID int64) ([]*Link, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var links []*Link
	for _, link := range db.data.Links {
		if link.PackageID == packageID {
			links = append(links, link)
		}
	}
	return links, nil
}

// GetLinksForProject returns all links for a project
func (db *DB) GetLinksForProject(projectID int64) ([]*Link, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var links []*Link
	for _, link := range db.data.Links {
		if link.ProjectID == projectID {
			links = append(links, link)
		}
	}
	return links, nil
}

// DeleteLink removes a link
func (db *DB) DeleteLink(packageID, projectID int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	for id, link := range db.data.Links {
		if link.PackageID == packageID && link.ProjectID == projectID {
			delete(db.data.Links, id)
			return db.save()
		}
	}
	return nil
}

// GetProjectsForPackage returns all projects linked to a package
func (db *DB) GetProjectsForPackage(packageID int64) ([]*Project, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var projects []*Project
	for _, link := range db.data.Links {
		if link.PackageID == packageID {
			if proj, ok := db.data.Projects[link.ProjectID]; ok {
				projects = append(projects, proj)
			}
		}
	}
	return projects, nil
}

// --- File operations ---

// InsertFiles inserts file entries for a package
func (db *DB) InsertFiles(packageID int64, files []*FileEntry) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Assign IDs
	for _, f := range files {
		f.ID = db.nextID()
		f.PackageID = packageID
	}

	db.data.Files[packageID] = files
	return db.save()
}

// GetFilesForPackage returns all files for a package
func (db *DB) GetFilesForPackage(packageID int64) ([]*FileEntry, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	return db.data.Files[packageID], nil
}
