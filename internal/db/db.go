package db

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pedrosousa13/lnpm/internal/debug"
	bolt "go.etcd.io/bbolt"
)

var (
	instance *DB
	once     sync.Once
)

// Bucket names
var (
	bucketPackages       = []byte("packages")
	bucketPackagesByName = []byte("packages_by_name")
	bucketProjects       = []byte("projects")
	bucketProjectsByPath = []byte("projects_by_path")
	bucketLinks          = []byte("links")
	bucketLinksByPackage = []byte("links_by_package")
	bucketLinksByProject = []byte("links_by_project")
	bucketFiles          = []byte("files")
	bucketMeta           = []byte("meta")
)

// DB wraps the bbolt database
type DB struct {
	db *bolt.DB
	mu sync.RWMutex
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
	ModTime      int64       `json:"mod_time"` // Unix nano timestamp for quick change detection
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
	debug.Logf("db: store path %s", storePath)

	// Ensure store directory exists
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	dbPath := filepath.Join(storePath, "lnpm.db")
	debug.Logf("db: opening %s", dbPath)

	// Open bbolt database
	boltDB, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{db: boltDB}

	// Initialize buckets
	err = boltDB.Update(func(tx *bolt.Tx) error {
		buckets := [][]byte{
			bucketPackages,
			bucketPackagesByName,
			bucketProjects,
			bucketProjectsByPath,
			bucketLinks,
			bucketLinksByPackage,
			bucketLinksByProject,
			bucketFiles,
			bucketMeta,
		}
		for _, bucket := range buckets {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return fmt.Errorf("failed to create bucket %s: %w", bucket, err)
			}
		}
		return nil
	})
	if err != nil {
		_ = boltDB.Close()
		return nil, err
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

// Close closes the database
func (db *DB) Close() error {
	return db.db.Close()
}

// Helper functions for ID encoding
func itob(v int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

func btoi(b []byte) int64 {
	return int64(binary.BigEndian.Uint64(b))
}

// nextID returns the next available ID
func (db *DB) nextID(tx *bolt.Tx) (int64, error) {
	b := tx.Bucket(bucketMeta)
	key := []byte("next_id")

	var id int64 = 1
	if v := b.Get(key); v != nil {
		id = btoi(v)
	}

	if err := b.Put(key, itob(id+1)); err != nil {
		return 0, err
	}

	return id, nil
}

// --- Package operations ---

// InsertPackage inserts or updates a package
func (db *DB) InsertPackage(pkg *Package) error {
	debug.Logf("db: insert package %s@%s", pkg.Name, pkg.Version)
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Update(func(tx *bolt.Tx) error {
		packages := tx.Bucket(bucketPackages)
		byName := tx.Bucket(bucketPackagesByName)

		// Check if package with same name exists (update case)
		if existingIDBytes := byName.Get([]byte(pkg.Name)); existingIDBytes != nil {
			existingID := btoi(existingIDBytes)
			if data := packages.Get(itob(existingID)); data != nil {
				var existing Package
				if err := json.Unmarshal(data, &existing); err == nil {
					// Update existing package
					existing.Version = pkg.Version
					existing.ContentHash = pkg.ContentHash
					existing.SourcePath = pkg.SourcePath
					existing.StorePath = pkg.StorePath
					existing.FilesCount = pkg.FilesCount
					existing.TotalSize = pkg.TotalSize
					existing.UpdatedAt = time.Now()
					pkg.ID = existing.ID

					data, err := json.Marshal(&existing)
					if err != nil {
						return err
					}
					return packages.Put(itob(existing.ID), data)
				}
			}
		}

		// Insert new package
		id, err := db.nextID(tx)
		if err != nil {
			return err
		}
		pkg.ID = id
		pkg.CreatedAt = time.Now()
		pkg.UpdatedAt = time.Now()

		data, err := json.Marshal(pkg)
		if err != nil {
			return err
		}

		if err := packages.Put(itob(id), data); err != nil {
			return err
		}

		// Index by name
		return byName.Put([]byte(pkg.Name), itob(id))
	})
}

// GetPackageByName returns the latest package with the given name
func (db *DB) GetPackageByName(name string) (*Package, error) {
	debug.Logf("db: get package by name %s", name)
	db.mu.RLock()
	defer db.mu.RUnlock()

	var pkg *Package
	err := db.db.View(func(tx *bolt.Tx) error {
		byName := tx.Bucket(bucketPackagesByName)
		packages := tx.Bucket(bucketPackages)

		idBytes := byName.Get([]byte(name))
		if idBytes == nil {
			return nil
		}

		data := packages.Get(idBytes)
		if data == nil {
			return nil
		}

		pkg = &Package{}
		return json.Unmarshal(data, pkg)
	})

	return pkg, err
}

// GetPackageByHash returns a package by its content hash
func (db *DB) GetPackageByHash(name, hash string) (*Package, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var result *Package
	err := db.db.View(func(tx *bolt.Tx) error {
		packages := tx.Bucket(bucketPackages)

		return packages.ForEach(func(k, v []byte) error {
			var pkg Package
			if err := json.Unmarshal(v, &pkg); err != nil {
				return nil // Skip invalid entries
			}
			if pkg.Name == name && pkg.ContentHash == hash {
				result = &pkg
			}
			return nil
		})
	})

	return result, err
}

// ListPackages returns all packages in the store
func (db *DB) ListPackages() ([]*Package, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var packages []*Package
	err := db.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketPackages)

		return b.ForEach(func(k, v []byte) error {
			var pkg Package
			if err := json.Unmarshal(v, &pkg); err != nil {
				return nil // Skip invalid entries
			}
			packages = append(packages, &pkg)
			return nil
		})
	})

	return packages, err
}

// DeletePackage deletes a package by ID
func (db *DB) DeletePackage(id int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Update(func(tx *bolt.Tx) error {
		packages := tx.Bucket(bucketPackages)
		byName := tx.Bucket(bucketPackagesByName)
		files := tx.Bucket(bucketFiles)

		// Get package name for index cleanup
		data := packages.Get(itob(id))
		if data != nil {
			var pkg Package
			if json.Unmarshal(data, &pkg) == nil {
				_ = byName.Delete([]byte(pkg.Name))
			}
		}

		_ = packages.Delete(itob(id))
		_ = files.Delete(itob(id))
		return nil
	})
}

// --- Project operations ---

// InsertProject inserts or updates a project
func (db *DB) InsertProject(proj *Project) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Update(func(tx *bolt.Tx) error {
		projects := tx.Bucket(bucketProjects)
		byPath := tx.Bucket(bucketProjectsByPath)

		// Check if project with same path exists (update case)
		if existingIDBytes := byPath.Get([]byte(proj.Path)); existingIDBytes != nil {
			existingID := btoi(existingIDBytes)
			if data := projects.Get(itob(existingID)); data != nil {
				var existing Project
				if err := json.Unmarshal(data, &existing); err == nil {
					existing.Name = proj.Name
					existing.PackageManager = proj.PackageManager
					existing.UpdatedAt = time.Now()
					proj.ID = existing.ID

					data, err := json.Marshal(&existing)
					if err != nil {
						return err
					}
					return projects.Put(itob(existing.ID), data)
				}
			}
		}

		// Insert new project
		id, err := db.nextID(tx)
		if err != nil {
			return err
		}
		proj.ID = id
		proj.CreatedAt = time.Now()
		proj.UpdatedAt = time.Now()

		data, err := json.Marshal(proj)
		if err != nil {
			return err
		}

		if err := projects.Put(itob(id), data); err != nil {
			return err
		}

		return byPath.Put([]byte(proj.Path), itob(id))
	})
}

// GetProjectByPath returns a project by its path
func (db *DB) GetProjectByPath(path string) (*Project, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var proj *Project
	err := db.db.View(func(tx *bolt.Tx) error {
		byPath := tx.Bucket(bucketProjectsByPath)
		projects := tx.Bucket(bucketProjects)

		idBytes := byPath.Get([]byte(path))
		if idBytes == nil {
			return nil
		}

		data := projects.Get(idBytes)
		if data == nil {
			return nil
		}

		proj = &Project{}
		return json.Unmarshal(data, proj)
	})

	return proj, err
}

// --- Link operations ---

// InsertLink creates a link between a package and project
func (db *DB) InsertLink(link *Link) error {
	debug.Logf("db: insert link pkg=%d proj=%d", link.PackageID, link.ProjectID)
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Update(func(tx *bolt.Tx) error {
		links := tx.Bucket(bucketLinks)
		byPackage := tx.Bucket(bucketLinksByPackage)
		byProject := tx.Bucket(bucketLinksByProject)

		// Check if link already exists
		var existingLinkID int64
		err := links.ForEach(func(k, v []byte) error {
			var l Link
			if err := json.Unmarshal(v, &l); err != nil {
				return nil
			}
			if l.PackageID == link.PackageID && l.ProjectID == link.ProjectID {
				existingLinkID = l.ID
			}
			return nil
		})
		if err != nil {
			return err
		}

		if existingLinkID > 0 {
			// Update existing link
			data := links.Get(itob(existingLinkID))
			if data != nil {
				var existing Link
				if json.Unmarshal(data, &existing) == nil {
					existing.LinkType = link.LinkType
					existing.UpdatedAt = time.Now()
					link.ID = existing.ID

					data, err := json.Marshal(&existing)
					if err != nil {
						return err
					}
					return links.Put(itob(existing.ID), data)
				}
			}
		}

		// Insert new link
		id, err := db.nextID(tx)
		if err != nil {
			return err
		}
		link.ID = id
		link.CreatedAt = time.Now()
		link.UpdatedAt = time.Now()

		data, err := json.Marshal(link)
		if err != nil {
			return err
		}

		if err := links.Put(itob(id), data); err != nil {
			return err
		}

		// Update indexes (store as JSON arrays of IDs)
		pkgKey := itob(link.PackageID)
		projKey := itob(link.ProjectID)

		// Add to package index
		var pkgLinks []int64
		if existing := byPackage.Get(pkgKey); existing != nil {
			_ = json.Unmarshal(existing, &pkgLinks)
		}
		pkgLinks = append(pkgLinks, id)
		pkgLinksData, _ := json.Marshal(pkgLinks)
		_ = byPackage.Put(pkgKey, pkgLinksData)

		// Add to project index
		var projLinks []int64
		if existing := byProject.Get(projKey); existing != nil {
			_ = json.Unmarshal(existing, &projLinks)
		}
		projLinks = append(projLinks, id)
		projLinksData, _ := json.Marshal(projLinks)
		_ = byProject.Put(projKey, projLinksData)

		return nil
	})
}

// GetLinksForPackage returns all links for a package
func (db *DB) GetLinksForPackage(packageID int64) ([]*Link, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var result []*Link
	err := db.db.View(func(tx *bolt.Tx) error {
		links := tx.Bucket(bucketLinks)
		byPackage := tx.Bucket(bucketLinksByPackage)

		data := byPackage.Get(itob(packageID))
		if data == nil {
			return nil
		}

		var linkIDs []int64
		if err := json.Unmarshal(data, &linkIDs); err != nil {
			return nil
		}

		for _, id := range linkIDs {
			linkData := links.Get(itob(id))
			if linkData != nil {
				var link Link
				if json.Unmarshal(linkData, &link) == nil {
					result = append(result, &link)
				}
			}
		}
		return nil
	})

	return result, err
}

// GetLinksForProject returns all links for a project
func (db *DB) GetLinksForProject(projectID int64) ([]*Link, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var result []*Link
	err := db.db.View(func(tx *bolt.Tx) error {
		links := tx.Bucket(bucketLinks)
		byProject := tx.Bucket(bucketLinksByProject)

		data := byProject.Get(itob(projectID))
		if data == nil {
			return nil
		}

		var linkIDs []int64
		if err := json.Unmarshal(data, &linkIDs); err != nil {
			return nil
		}

		for _, id := range linkIDs {
			linkData := links.Get(itob(id))
			if linkData != nil {
				var link Link
				if json.Unmarshal(linkData, &link) == nil {
					result = append(result, &link)
				}
			}
		}
		return nil
	})

	return result, err
}

// DeleteLink removes a link
func (db *DB) DeleteLink(packageID, projectID int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Update(func(tx *bolt.Tx) error {
		links := tx.Bucket(bucketLinks)
		byPackage := tx.Bucket(bucketLinksByPackage)
		byProject := tx.Bucket(bucketLinksByProject)

		// Find the link ID
		var linkID int64
		_ = links.ForEach(func(k, v []byte) error {
			var l Link
			if json.Unmarshal(v, &l) == nil {
				if l.PackageID == packageID && l.ProjectID == projectID {
					linkID = l.ID
				}
			}
			return nil
		})

		if linkID == 0 {
			return nil
		}

		// Delete the link
		_ = links.Delete(itob(linkID))

		// Update package index
		pkgKey := itob(packageID)
		if data := byPackage.Get(pkgKey); data != nil {
			var ids []int64
			_ = json.Unmarshal(data, &ids)
			newIDs := make([]int64, 0, len(ids))
			for _, id := range ids {
				if id != linkID {
					newIDs = append(newIDs, id)
				}
			}
			if len(newIDs) > 0 {
				newData, _ := json.Marshal(newIDs)
				_ = byPackage.Put(pkgKey, newData)
			} else {
				_ = byPackage.Delete(pkgKey)
			}
		}

		// Update project index
		projKey := itob(projectID)
		if data := byProject.Get(projKey); data != nil {
			var ids []int64
			_ = json.Unmarshal(data, &ids)
			newIDs := make([]int64, 0, len(ids))
			for _, id := range ids {
				if id != linkID {
					newIDs = append(newIDs, id)
				}
			}
			if len(newIDs) > 0 {
				newData, _ := json.Marshal(newIDs)
				_ = byProject.Put(projKey, newData)
			} else {
				_ = byProject.Delete(projKey)
			}
		}

		return nil
	})
}

// GetProjectsForPackage returns all projects linked to a package
func (db *DB) GetProjectsForPackage(packageID int64) ([]*Project, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var result []*Project
	err := db.db.View(func(tx *bolt.Tx) error {
		links := tx.Bucket(bucketLinks)
		byPackage := tx.Bucket(bucketLinksByPackage)
		projects := tx.Bucket(bucketProjects)

		data := byPackage.Get(itob(packageID))
		if data == nil {
			return nil
		}

		var linkIDs []int64
		if err := json.Unmarshal(data, &linkIDs); err != nil {
			return nil
		}

		for _, id := range linkIDs {
			linkData := links.Get(itob(id))
			if linkData != nil {
				var link Link
				if json.Unmarshal(linkData, &link) == nil {
					projData := projects.Get(itob(link.ProjectID))
					if projData != nil {
						var proj Project
						if json.Unmarshal(projData, &proj) == nil {
							result = append(result, &proj)
						}
					}
				}
			}
		}
		return nil
	})

	return result, err
}

// --- File operations ---

// InsertFiles inserts file entries for a package
func (db *DB) InsertFiles(packageID int64, files []*FileEntry) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Batch(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketFiles)

		// Assign IDs - pre-allocate to avoid multiple ID generations
		startID, err := db.nextID(tx)
		if err != nil {
			return err
		}

		// Batch assign IDs
		for i, f := range files {
			f.ID = startID + int64(i)
			f.PackageID = packageID
		}

		// Update next ID counter once for all files
		meta := tx.Bucket(bucketMeta)
		if err := meta.Put([]byte("next_id"), itob(startID+int64(len(files)))); err != nil {
			return err
		}

		data, err := json.Marshal(files)
		if err != nil {
			return err
		}

		return b.Put(itob(packageID), data)
	})
}

// GetFilesForPackage returns all files for a package
func (db *DB) GetFilesForPackage(packageID int64) ([]*FileEntry, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var files []*FileEntry
	err := db.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketFiles)

		data := b.Get(itob(packageID))
		if data == nil {
			return nil
		}

		return json.Unmarshal(data, &files)
	})

	return files, err
}

// GetFilesForPackageByName returns files using package name lookup
func (db *DB) GetFilesForPackageByName(name string) ([]*FileEntry, error) {
	pkg, err := db.GetPackageByName(name)
	if err != nil || pkg == nil {
		return nil, err
	}
	return db.GetFilesForPackage(pkg.ID)
}
