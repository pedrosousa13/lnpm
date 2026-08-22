package db

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/debug"
	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

var (
	instance *DB
	initErr  error
	once     sync.Once
)

// openTimeout is how long bolt.Open waits for the bbolt file lock before it
// gives up. The lock serializes every lnpm process against the shared store,
// so this has to cover a whole in-flight command (a publish or a push) rather
// than a single transaction — otherwise parallel invocations from a task
// runner all die while the first one is still working.
//
// It is a var rather than a const purely so tests can shorten it: the
// production value is long enough that a test which waits it out would stall
// the suite for half a minute.
var openTimeout = 30 * time.Second

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
	bucketTags           = []byte("tags")
)

// DefaultTag is the tag a publish moves and the one every name-based lookup
// resolves through: bucketPackagesByName always names the version it points at.
const DefaultTag = "latest"

// tagSeparator joins a package name and a tag into one bucket key. A package
// name cannot contain a NUL byte, so the first one in a key always ends the
// name — which is what lets the tags of one package be found by prefix.
const tagSeparator = "\x00"

func tagKey(name, tag string) []byte {
	return []byte(name + tagSeparator + tag)
}

func tagPrefix(name string) []byte {
	return []byte(name + tagSeparator)
}

// schemaVersion is the shape of the buckets this build writes. It is recorded in
// bucketMeta so a database written by an older build can be brought forward on
// open rather than silently read as if it were current.
//
//	1 — packages, one live record per name, no tags bucket.
//	2 — a tags bucket, with the latest tag naming the record bucketPackagesByName
//	    points at.
const schemaVersion = 2

var keySchemaVersion = []byte("schema_version")

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
	ID        int64  `json:"id"`
	PackageID int64  `json:"package_id"`
	ProjectID int64  `json:"project_id"`
	LinkType  string `json:"link_type"`
	// Tag is the channel the project follows. When that tag is moved to
	// another version - by a publish, or by tagging a version already in the
	// store - this link is carried across to it, so the project keeps
	// consuming the package it asked for rather than the release it happened
	// to be pinned to. Empty means DefaultTag, which is what every link
	// written before tags existed meant.
	Tag       string    `json:"tag,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// tag returns the channel a link follows, reading an unset tag as the default
// one. Links written before tags existed carry none.
func (l *Link) tag() string {
	if l.Tag == "" {
		return DefaultTag
	}
	return l.Tag
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

// GetDB returns the singleton database instance.
//
// initDB runs once, so its error is kept in package state and handed to every
// later caller too. A caller that got nil back with no error would panic on
// first use instead of reporting why the database could not be opened.
func GetDB() (*DB, error) {
	once.Do(func() {
		instance, initErr = initDB()
	})
	return instance, initErr
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
	boltDB, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		// bbolt reports a lost race for the file lock as ErrTimeout. Name the
		// likely cause, because "timeout" on its own tells the user nothing.
		if errors.Is(err, bolterrors.ErrTimeout) {
			return nil, fmt.Errorf("another lnpm process appears to be running (database is locked): %w", err)
		}
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
			bucketTags,
		}
		for _, bucket := range buckets {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return fmt.Errorf("failed to create bucket %s: %w", bucket, err)
			}
		}
		return migrateTx(tx)
	})
	if err != nil {
		_ = boltDB.Close()
		return nil, err
	}

	return db, nil
}

// migrateTx brings a database written by an older build up to schemaVersion,
// inside the transaction that created any buckets it was missing.
//
// Version 1 had no tags bucket: bucketPackagesByName was the only way to reach a
// package, and the one record it named was by definition that package's latest.
// So every name in that index gains a latest tag naming its record's content
// hash, and nothing else changes. No record is rewritten and nothing is deleted,
// which is what makes the pass safe to interrupt — bolt commits it whole or not
// at all, and re-running it writes the same entries again.
//
// A database written by a newer build is refused, not migrated backwards and not
// merely left alone. Guessing at a schema this build does not know would be
// guessing about which files gc may delete - and declining to migrate while
// letting the session carry on is exactly that guess, made silently: the very
// next command would read the store through this build's rules. Refusing at open
// is the only place the guess can be avoided rather than deferred.
func migrateTx(tx *bolt.Tx) error {
	meta := tx.Bucket(bucketMeta)
	if v := meta.Get(keySchemaVersion); len(v) == 8 {
		switch recorded := btoi(v); {
		case recorded > schemaVersion:
			return fmt.Errorf("the store was written by a newer lnpm (schema version %d; this build understands %d): upgrade lnpm to use it", recorded, schemaVersion)
		case recorded == schemaVersion:
			return nil
		}
	}

	packages := tx.Bucket(bucketPackages)
	tags := tx.Bucket(bucketTags)

	err := tx.Bucket(bucketPackagesByName).ForEach(func(name, idBytes []byte) error {
		data := packages.Get(idBytes)
		if data == nil {
			// An index entry naming no record: there is no hash to tag, and
			// inventing one would create a tag pointing at nothing.
			return nil
		}
		var pkg Package
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil // Unreadable record, left exactly as it was found
		}
		return tags.Put(tagKey(string(name), DefaultTag), []byte(pkg.ContentHash))
	})
	if err != nil {
		return fmt.Errorf("failed to record the %s tag of existing packages: %w", DefaultTag, err)
	}

	return meta.Put(keySchemaVersion, itob(schemaVersion))
}

// getStorePath returns the lnpm store path
func getStorePath() (string, error) {
	return config.GetStorePath()
}

// Close closes the database
func (db *DB) Close() error {
	return db.db.Close()
}

// LockHeld reports whether the exclusive file lock bolt.Open took is still
// held. bbolt takes it for the whole life of the handle and drops it only in
// Close, which also clears the path this reads, so an open handle means no
// other lnpm process can be writing to the store.
//
// This exists for callers whose safety rests on that rather than on their own
// bookkeeping — gc's sweep for temp directories left by an interrupted publish
// or relink — so they can assert the invariant instead of assuming it, and fail
// loudly if a later change moves them outside the window where it holds.
// It takes no lock of its own. db.mu would guard nothing here, because Close
// does not take it either — so holding it would suggest a synchronisation that
// does not exist. The callers that matter run on the goroutine that would do the
// closing.
func (db *DB) LockHeld() bool {
	return db.db.Path() != ""
}

// ResetForTesting resets the singleton instance for testing
// This should only be used in tests
func ResetForTesting() {
	if instance != nil {
		_ = instance.Close()
	}
	instance = nil
	initErr = nil
	once = sync.Once{}
}

// normalizePath normalizes a path to handle symlink issues consistently
// On macOS, /var is a symlink to /private/var, so we normalize both cases
func normalizePath(path string) string {
	// Try to resolve symlinks if path exists
	if realPath, err := filepath.EvalSymlinks(path); err == nil {
		return realPath
	}

	// For non-existent paths on macOS, manually handle /var -> /private/var
	if !strings.HasPrefix(path, "/private/") && strings.HasPrefix(path, "/var/") {
		return "/private" + path
	}

	return filepath.Clean(path)
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

// InsertPackage inserts or updates a package.
//
// A package whose file manifest is being written at the same time must go
// through InsertPackageWithFiles instead: the two rows describe one generation
// of one package, and committing them separately lets a failure between them
// leave a package row naming content its file rows do not describe.
func (db *DB) InsertPackage(pkg *Package) error {
	return db.InsertPackageTagged(pkg, DefaultTag)
}

// InsertPackageTagged is InsertPackage, pointing tag at the package it records
// instead of the default tag.
func (db *DB) InsertPackageTagged(pkg *Package, tag string) error {
	debug.Logf("db: insert package %s@%s (tag: %s)", pkg.Name, pkg.Version, tag)
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Update(func(tx *bolt.Tx) error {
		return db.insertPackageTx(tx, pkg, tag)
	})
}

// InsertPackageWithFiles records a package and the files it contains in a single
// transaction, so the two either both land or neither does.
//
// This is not tidiness. A relink decides which files it can leave alone by
// comparing the hashes in the file rows against the ones it last linked into the
// project, so a package row that names a new generation while the file rows
// still describe the previous one is enough to mark a genuinely changed file
// reusable and carry stale content into a consumer's project. Bolt gives one
// transaction per write, and one transaction is what makes that state
// unreachable.
func (db *DB) InsertPackageWithFiles(pkg *Package, files []*FileEntry) error {
	return db.InsertPackageWithFilesTagged(pkg, files, DefaultTag)
}

// InsertPackageWithFilesTagged is InsertPackageWithFiles, pointing tag at the
// package it records instead of the default tag.
func (db *DB) InsertPackageWithFilesTagged(pkg *Package, files []*FileEntry, tag string) error {
	debug.Logf("db: insert package %s@%s with %d files (tag: %s)", pkg.Name, pkg.Version, len(files), tag)
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Update(func(tx *bolt.Tx) error {
		if err := db.insertPackageTx(tx, pkg, tag); err != nil {
			return err
		}
		return db.insertFilesTx(tx, pkg.ID, files)
	})
}

// insertPackageTx is InsertPackage's body, taking the transaction from its
// caller so it can share one with insertFilesTx.
func (db *DB) insertPackageTx(tx *bolt.Tx, pkg *Package, tag string) error {
	packages := tx.Bucket(bucketPackages)

	// A record is addressed by name and content hash. Publishing the same
	// content again updates that record in place; publishing different content
	// adds a version beside the ones already there instead of displacing them,
	// which is what lets two tags name genuinely different content. Which
	// version a name resolves to is decided by the tag, below, and not by which
	// record was written last.
	//
	// The update overwrites Version, which is what one record addressed by
	// content forces: the same bytes cannot be two versions at once, so the
	// newer publish's label is the one kept. Normally that is the only version
	// there is, because package.json is inside the hash. It is not always:
	// pack's walk tests the ignore rules before the default-include list, and
	// the default-include list is consulted only under a "files" whitelist, so
	// an .npmignore - or a .gitignore through the fallback - with a line reading
	// package.json drops it from the pack, and two publishes differing only in
	// version then hash the same. What that costs is a report: `lnpm status`
	// shows the record under the newer version while the tag that named it has
	// not moved. No consumer is affected, because the content really is
	// identical. The fix belongs in pack, which unlike npm lets package.json be
	// ignored at all.
	if existing := findPackageByHashTx(tx, pkg.Name, pkg.ContentHash); existing != nil {
		existing.Version = pkg.Version
		existing.SourcePath = pkg.SourcePath
		existing.StorePath = pkg.StorePath
		existing.FilesCount = pkg.FilesCount
		existing.TotalSize = pkg.TotalSize
		existing.UpdatedAt = time.Now()
		pkg.ID = existing.ID
		pkg.CreatedAt = existing.CreatedAt

		data, err := json.Marshal(existing)
		if err != nil {
			return err
		}
		if err := packages.Put(itob(existing.ID), data); err != nil {
			return err
		}
		return setTagTx(tx, pkg.Name, tag, pkg.ContentHash, pkg.ID)
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

	return setTagTx(tx, pkg.Name, tag, pkg.ContentHash, pkg.ID)
}

// --- Tag operations ---

// setTagTx points name's tag at hash, which belongs to the record with the given
// id, and carries the projects that follow that tag across to it.
//
// Setting the default tag also moves the name index, because the two say the
// same thing: bucketPackagesByName names the version tagged DefaultTag. Letting
// them disagree would give GetPackageByName and ResolveTag different answers to
// the same question.
//
// A package's first version is given the default tag as well as the one asked
// for. Every command but a tag-aware add reaches a package through the name
// index, so a package published only under some other tag would sit in the store
// and on disk while push, remove, restore and status could not see it at all.
//
// That is a guarantee about what publishing does, not an invariant of the
// database. gc can reach the state this avoids and does so deliberately: it
// collects the version the default tag names when nothing links it, because that
// tag is not a reachability root (ADR-0002), and DeletePackage then clears the
// name index. What is left is a package reachable only by its other tags. The
// next publish of that name restores the default tag, so the state is one gc can
// produce and only a publish can clear.
func setTagTx(tx *bolt.Tx, name, tag, hash string, id int64) error {
	if tag == "" {
		tag = DefaultTag
	}

	tags := tx.Bucket(bucketTags)
	byName := tx.Bucket(bucketPackagesByName)

	// Whatever the tag named before this, so the projects following it can be
	// carried across. Read before the Put that overwrites it.
	var previous *Package
	if prevHash := tags.Get(tagKey(name, tag)); prevHash != nil && string(prevHash) != hash {
		previous = findPackageByHashTx(tx, name, string(prevHash))
	}

	if err := tags.Put(tagKey(name, tag), []byte(hash)); err != nil {
		return err
	}

	if tag == DefaultTag {
		if err := byName.Put([]byte(name), itob(id)); err != nil {
			return err
		}
	} else if byName.Get([]byte(name)) == nil {
		if err := setTagTx(tx, name, DefaultTag, hash, id); err != nil {
			return err
		}
	}

	if previous == nil || previous.ID == id {
		return nil
	}
	return moveLinksTx(tx, previous.ID, id, tag)
}

// moveLinksTx repoints the links that follow tag from one version of a package
// to another.
//
// A link says a project consumes a package, and every command that reads one -
// push, publish --push, remove, retreat, status - finds it by looking the
// package up by name. Leaving a link on the version a tag has moved off would
// make those projects unreachable from the name they were linked under, so a
// push would report no consumers and a remove would find nothing to unlink.
//
// Only the links following the tag that moved are carried across: a project that
// asked for beta must not be dragged onto latest because the two tags happened
// to name the same version. A project that already has a link on the
// destination keeps that one and loses the duplicate, because everything that
// reads links treats one row per project and package as given.
func moveLinksTx(tx *bolt.Tx, fromID, toID int64, tag string) error {
	links := tx.Bucket(bucketLinks)
	byPackage := tx.Bucket(bucketLinksByPackage)
	byProject := tx.Bucket(bucketLinksByProject)

	// Both index entries are read before anything moves, and an unreadable one
	// abandons the tag move rather than being treated as empty. Treating the
	// source as empty would leave every consumer on the version the tag moved
	// off; treating the destination as empty would rewrite its entry from the
	// links carried across alone, dropping the ones already there.
	fromIDs, err := indexIDs(byPackage, itob(fromID))
	if err != nil {
		return err
	}
	if len(fromIDs) == 0 {
		return nil
	}

	toIDs, err := indexIDs(byPackage, itob(toID))
	if err != nil {
		return err
	}
	onDestination := make(map[int64]bool, len(toIDs))
	for _, id := range toIDs {
		if data := links.Get(itob(id)); data != nil {
			var l Link
			if json.Unmarshal(data, &l) == nil {
				onDestination[l.ProjectID] = true
			}
		}
	}

	stay := make([]int64, 0, len(fromIDs))
	for _, id := range fromIDs {
		data := links.Get(itob(id))
		if data == nil {
			continue // Index entry naming no link row: drop it
		}
		var l Link
		if err := json.Unmarshal(data, &l); err != nil || l.tag() != tag {
			stay = append(stay, id)
			continue
		}

		if onDestination[l.ProjectID] {
			// The project already follows the destination version.
			if err := links.Delete(itob(id)); err != nil {
				return err
			}
			removeIDFromIndex(byProject, itob(l.ProjectID), id)
			continue
		}

		l.PackageID = toID
		l.UpdatedAt = time.Now()
		moved, err := json.Marshal(&l)
		if err != nil {
			return err
		}
		if err := links.Put(itob(id), moved); err != nil {
			return err
		}
		onDestination[l.ProjectID] = true
		toIDs = append(toIDs, id)
	}

	if err := putIndexIDs(byPackage, itob(fromID), stay); err != nil {
		return err
	}
	return putIndexIDs(byPackage, itob(toID), toIDs)
}

// indexIDs reads a link index entry, which holds a JSON array of link IDs. A
// key that is not there is not an error: it is a package or project nothing
// links, which is the ordinary state of most of them.
//
// An entry that will not parse is an error, and the distinction is the whole of
// #329. Returning no IDs for it made "this package has no consumers" and "this
// package's consumers could not be read" the same answer, and gc acts on the
// first by deleting the version. Per ADR-0001 the direction is what decides: a
// swallowed error that widens what a destructive pass removes is a bug, so this
// one is reported rather than absorbed. The opposite direction is left alone -
// ListPackages still skips a package record it cannot parse, which leaks a store
// entry instead of deleting one.
func indexIDs(b *bolt.Bucket, key []byte) ([]int64, error) {
	data := b.Get(key)
	if data == nil {
		return nil, nil
	}
	var ids []int64
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, fmt.Errorf("link index entry for key %d is unreadable: %w", btoi(key), err)
	}
	return ids, nil
}

// putIndexIDs writes a link index entry, deleting the key when no IDs are left
// so an empty index entry never outlives the links it named.
func putIndexIDs(b *bolt.Bucket, key []byte, ids []int64) error {
	if len(ids) == 0 {
		return b.Delete(key)
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return b.Put(key, data)
}

// SetTag points a tag at a version already in the store, without republishing
// it. The version is named by its content hash, and one that no record has is
// refused: a tag pointing at nothing resolves to nothing, and once gc treats
// tags as reachability roots it would also protect nothing.
func (db *DB) SetTag(name, tag, hash string) error {
	debug.Logf("db: set tag %s of %s to %s", tag, name, hash)
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Update(func(tx *bolt.Tx) error {
		pkg := findPackageByHashTx(tx, name, hash)
		if pkg == nil {
			return fmt.Errorf("no version of %s with content hash %s is in the store", name, hash)
		}
		return setTagTx(tx, name, tag, hash, pkg.ID)
	})
}

// DeleteTag removes a tag from a package, leaving the version it named in the
// store.
//
// The default tag cannot be removed. It is what bucketPackagesByName mirrors, so
// deleting it would leave the package published, its files on disk and its
// record intact, while making it unreachable by name from every command lnpm
// has.
//
// gc can leave a package in that state anyway, by collecting the version the
// default tag names while another tag keeps an older one (ADR-0002). The refusal
// here is not a claim that the state is unreachable, then. It is that reaching
// it by deleting a tag would be a user asking for a package to stay published
// and getting it hidden instead, where reaching it through gc is a version being
// collected because nothing reached it - which is what gc is for, and which the
// next publish of that name undoes.
func (db *DB) DeleteTag(name, tag string) error {
	if tag == "" {
		tag = DefaultTag
	}
	if tag == DefaultTag {
		return fmt.Errorf("cannot delete the %s tag of %s: it is the tag every lookup by name resolves through", DefaultTag, name)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTags).Delete(tagKey(name, tag))
	})
}

// TagsForPackage returns every tag set on name, as tag to content hash.
func (db *DB) TagsForPackage(name string) (map[string]string, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	tags := make(map[string]string)
	err := db.db.View(func(tx *bolt.Tx) error {
		prefix := tagPrefix(name)
		c := tx.Bucket(bucketTags).Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			tags[string(k[len(prefix):])] = string(v)
		}
		return nil
	})

	return tags, err
}

// ResolveTag returns the version a tag names, or nil when the tag is not set.
// An empty tag means the default one.
func (db *DB) ResolveTag(name, tag string) (*Package, error) {
	if tag == "" {
		tag = DefaultTag
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	var pkg *Package
	err := db.db.View(func(tx *bolt.Tx) error {
		hash := tx.Bucket(bucketTags).Get(tagKey(name, tag))
		if hash == nil {
			return nil
		}
		pkg = findPackageByHashTx(tx, name, string(hash))
		return nil
	})

	return pkg, err
}

// findPackageByHashTx returns the record for one version of a package, or nil if
// the store holds no such version.
func findPackageByHashTx(tx *bolt.Tx, name, hash string) *Package {
	var found *Package
	_ = tx.Bucket(bucketPackages).ForEach(func(k, v []byte) error {
		if found != nil {
			return nil
		}
		var pkg Package
		if err := json.Unmarshal(v, &pkg); err != nil {
			return nil // Skip invalid entries
		}
		if pkg.Name == name && pkg.ContentHash == hash {
			found = &pkg
		}
		return nil
	})
	return found
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
		result = findPackageByHashTx(tx, name, hash)
		return nil
	})

	return result, err
}

// GetPackageVersions returns every version of a package the store still holds,
// most recently published first.
//
// GetPackageByName answers a different question: it resolves the name index,
// which mirrors the default tag, so it names one row of a history that now has
// several. This is the whole history — exactly the set gc has not collected,
// because a version's record and its store entry go together.
//
// The order is UpdatedAt descending, and UpdatedAt rather than CreatedAt because
// that is the timestamp the rest of lnpm calls a package's publish time:
// `lnpm status` prints it under PUBLISHED and gc measures --older-than against
// it. A listing that sorted or reported the other one would disagree with the
// command that acts on it.
//
// The tie-break on ID is not decoration. Bolt stamps UpdatedAt from time.Now,
// and two publishes can land inside one tick of a coarse clock — Windows' is
// about 15ms — so without it the order would be arbitrary precisely when two
// versions are hardest to tell apart, and would differ between runs over an
// unchanged store.
//
// A record that will not unmarshal fails the whole lookup, unlike the listings
// alongside it, which skip one. A version that disappears from a rollback
// history is indistinguishable on screen from one gc collected, and the same
// lookup answers `lnpm add <pkg>@<hash>`, so a skipped record would have the
// user told the build they are rolling back to does not exist. GetPackageByName,
// which the add path went through before this existed, already surfaces a record
// it cannot parse. The cost is that the failure is not scoped to name: a damaged
// record carries no readable name, so any one of them fails every history. That
// is a damaged store rather than a state lnpm writes.
func (db *DB) GetPackageVersions(name string) ([]*Package, error) {
	debug.Logf("db: get versions of %s", name)
	db.mu.RLock()
	defer db.mu.RUnlock()

	var versions []*Package
	err := db.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketPackages).ForEach(func(k, v []byte) error {
			var pkg Package
			if err := json.Unmarshal(v, &pkg); err != nil {
				return fmt.Errorf("package record %d will not parse: %w", btoi(k), err)
			}
			if pkg.Name == name {
				versions = append(versions, &pkg)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(versions, func(i, j int) bool {
		if !versions[i].UpdatedAt.Equal(versions[j].UpdatedAt) {
			return versions[i].UpdatedAt.After(versions[j].UpdatedAt)
		}
		return versions[i].ID > versions[j].ID
	})
	return versions, nil
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
		links := tx.Bucket(bucketLinks)
		byPackage := tx.Bucket(bucketLinksByPackage)
		byProject := tx.Bucket(bucketLinksByProject)

		// Clean up the indexes that name this version.
		//
		// The name index is cleared only when it names this very record: a
		// package can have several versions now, and dropping the entry while
		// collecting a superseded one would unpublish a package whose current
		// version is still in the store.
		data := packages.Get(itob(id))
		if data != nil {
			var pkg Package
			if json.Unmarshal(data, &pkg) == nil {
				if current := byName.Get([]byte(pkg.Name)); current != nil && btoi(current) == id {
					_ = byName.Delete([]byte(pkg.Name))
				}
				deleteTagsForHashTx(tx, pkg.Name, pkg.ContentHash)
			}
		}

		// Clean up any links referencing this package so we don't leave
		// orphaned link rows or dangling index entries.
		pkgKey := itob(id)
		if linkData := byPackage.Get(pkgKey); linkData != nil {
			var linkIDs []int64
			_ = json.Unmarshal(linkData, &linkIDs)
			for _, linkID := range linkIDs {
				// Determine the project this link belonged to, then scrub the
				// link ID from that project's index.
				if ld := links.Get(itob(linkID)); ld != nil {
					var l Link
					if json.Unmarshal(ld, &l) == nil {
						removeIDFromIndex(byProject, itob(l.ProjectID), linkID)
					}
				}
				_ = links.Delete(itob(linkID))
			}
			_ = byPackage.Delete(pkgKey)
		}

		_ = packages.Delete(itob(id))
		_ = files.Delete(itob(id))
		return nil
	})
}

// deleteTagsForHashTx removes every tag of name that points at hash. A tag left
// naming a deleted version resolves to nothing and, since tags are what keeps a
// version from being collected, would go on protecting nothing.
//
// The keys are collected before any is deleted rather than deleted through the
// cursor, so the iteration does not depend on how bolt handles a cursor whose
// current key has just been removed.
func deleteTagsForHashTx(tx *bolt.Tx, name, hash string) {
	tags := tx.Bucket(bucketTags)
	prefix := tagPrefix(name)

	var stale [][]byte
	c := tags.Cursor()
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		if string(v) == hash {
			stale = append(stale, append([]byte(nil), k...))
		}
	}
	for _, k := range stale {
		_ = tags.Delete(k)
	}
}

// packageNameTx returns the name of the package a record ID names, or "" when
// no readable record has that ID.
func packageNameTx(tx *bolt.Tx, id int64) string {
	data := tx.Bucket(bucketPackages).Get(itob(id))
	if data == nil {
		return ""
	}
	var pkg Package
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Name
}

// deleteLinkRowTx removes one link row and scrubs it from both indexes, so no
// index entry outlives the row it named.
func deleteLinkRowTx(tx *bolt.Tx, linkID, packageID, projectID int64) error {
	if err := tx.Bucket(bucketLinks).Delete(itob(linkID)); err != nil {
		return err
	}
	removeIDFromIndex(tx.Bucket(bucketLinksByPackage), itob(packageID), linkID)
	removeIDFromIndex(tx.Bucket(bucketLinksByProject), itob(projectID), linkID)
	return nil
}

// removeIDFromIndex removes id from the []int64 stored at key in bucket b,
// deleting the key entirely when the slice becomes empty.
func removeIDFromIndex(b *bolt.Bucket, key []byte, id int64) {
	data := b.Get(key)
	if data == nil {
		return
	}
	var ids []int64
	if json.Unmarshal(data, &ids) != nil {
		return
	}
	newIDs := make([]int64, 0, len(ids))
	for _, existing := range ids {
		if existing != id {
			newIDs = append(newIDs, existing)
		}
	}
	if len(newIDs) > 0 {
		newData, _ := json.Marshal(newIDs)
		_ = b.Put(key, newData)
	} else {
		_ = b.Delete(key)
	}
}

// GetProjectByID returns a project by its ID, or nil if not found.
func (db *DB) GetProjectByID(id int64) (*Project, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var proj *Project
	err := db.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketProjects).Get(itob(id))
		if data == nil {
			return nil
		}
		proj = &Project{}
		return json.Unmarshal(data, proj)
	})

	return proj, err
}

// --- Project operations ---

// InsertProject inserts or updates a project
func (db *DB) InsertProject(proj *Project) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Normalize path to avoid symlink issues (e.g., /var vs /private/var on macOS)
	proj.Path = normalizePath(proj.Path)

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

	// Normalize path to avoid symlink issues (e.g., /var vs /private/var on macOS)
	path = normalizePath(path)

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

// InsertLink records that a project consumes a version of a package, replacing
// whatever it consumed of that package before.
//
// A project holds one row per package name, and that is an invariant this
// function is responsible for rather than one its callers happen to respect.
// Everything downstream assumes it: moveLinksTx says so outright, linksOfProject
// keys by name and would silently drop all but one of several rows, and remove
// and retreat delete the one row that map hands them. A project has one
// .lnpm/<name> and so consumes one version of one name; a second row is a second
// answer to which.
//
// It is enforced here because the path that would break it is the one the whole
// feature exists for. `lnpm add pkg@beta` in a project already on latest either
// finds the row - when both tags name one version, which is what `lnpm tag`
// produces - or does not, when they name two. Both have to end with one row
// carrying the tag that was asked for: the first because a row updated without
// its tag leaves the project recorded as a latest follower, which the next
// publish drags forward; the second because the stale row goes on being carried
// onto each new release, and `publish --push` then overwrites the channel
// consumer's files with what latest names.
func (db *DB) InsertLink(link *Link) error {
	debug.Logf("db: insert link pkg=%d proj=%d tag=%q", link.PackageID, link.ProjectID, link.Tag)
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Update(func(tx *bolt.Tx) error {
		links := tx.Bucket(bucketLinks)
		byPackage := tx.Bucket(bucketLinksByPackage)
		byProject := tx.Bucket(bucketLinksByProject)

		// The name the incoming link is for, so a row this project holds on
		// another version of it can be recognised. An unreadable package record
		// leaves this empty, which matches no row and so falls back to the
		// same-record update alone rather than deleting on a guess.
		name := packageNameTx(tx, link.PackageID)

		// indexIDs decodes into a slice of its own, so deleting rows below does
		// not disturb this walk.
		//
		// An unreadable index entry refuses the insert instead of walking no
		// rows. This walk is what keeps one row per project and package name:
		// skipping it would leave the row this link replaces in place, and the
		// project would then hold two answers to which version it consumes -
		// the state the doc comment above says everything downstream assumes
		// away.
		existingIDs, err := indexIDs(byProject, itob(link.ProjectID))
		if err != nil {
			return err
		}
		var updated bool
		for _, id := range existingIDs {
			data := links.Get(itob(id))
			if data == nil {
				continue // Index entry naming no link row
			}
			var existing Link
			if json.Unmarshal(data, &existing) != nil {
				continue
			}

			switch {
			case existing.PackageID == link.PackageID:
				existing.LinkType = link.LinkType
				// The tag too. Without it a project that switches channel onto
				// the version it is already on keeps following the old one.
				existing.Tag = link.Tag
				existing.UpdatedAt = time.Now()
				link.ID = existing.ID

				encoded, err := json.Marshal(&existing)
				if err != nil {
					return err
				}
				if err := links.Put(itob(existing.ID), encoded); err != nil {
					return err
				}
				updated = true

			case name != "" && packageNameTx(tx, existing.PackageID) == name:
				// Another version of the same package: the row this link
				// replaces.
				if err := deleteLinkRowTx(tx, existing.ID, existing.PackageID, existing.ProjectID); err != nil {
					return err
				}
			}
		}
		if updated {
			return nil
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

// GetLinksForPackage returns every link a package has, and reports rather than
// hides any it could not read.
//
// This is gc's reachability check, so the three ways the answer can be short - an
// unreadable index entry, an index entry naming a row that is not there, and a
// row that will not parse - all have to be errors. Each of them otherwise leaves
// a consumer out of a list gc reads as "nothing is using this version", and gc
// then deletes the store entry that project is linked to and reports success.
// That is the whole of #329, and ADR-0001 is the rule it is decided by: a
// swallowed error that widens a destructive set is a bug.
//
// The index entry naming no row is included even though no flow lnpm has
// produces one. Every path that deletes a link row - moveLinksTx, DeletePackage,
// deleteLinkRowTx, DeleteLink - scrubs the ID from both indexes inside the same
// bolt transaction, and bolt commits a transaction whole or not at all, so the
// state is unreachable except through damage to the file. Tolerating it would be
// tolerating exactly the class of damage the other two cases refuse.
//
// Returning the links read so far alongside the error was rejected: a caller
// that checks the error is no better off for them, and one that does not gets a
// short list that looks complete, which is the bug.
func (db *DB) GetLinksForPackage(packageID int64) ([]*Link, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var result []*Link
	err := db.db.View(func(tx *bolt.Tx) error {
		links := tx.Bucket(bucketLinks)
		byPackage := tx.Bucket(bucketLinksByPackage)

		linkIDs, err := indexIDs(byPackage, itob(packageID))
		if err != nil {
			return err
		}

		for _, id := range linkIDs {
			linkData := links.Get(itob(id))
			if linkData == nil {
				return fmt.Errorf("link index names link %d, which is not in the database", id)
			}
			var link Link
			if err := json.Unmarshal(linkData, &link); err != nil {
				return fmt.Errorf("link %d is unreadable: %w", id, err)
			}
			result = append(result, &link)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
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

// InsertFiles inserts file entries for a package.
//
// A package being written at the same time must go through
// InsertPackageWithFiles instead, for the reason given there.
func (db *DB) InsertFiles(packageID int64, files []*FileEntry) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.db.Batch(func(tx *bolt.Tx) error {
		return db.insertFilesTx(tx, packageID, files)
	})
}

// insertFilesTx is InsertFiles' body, taking the transaction from its caller so
// it can share one with insertPackageTx.
func (db *DB) insertFilesTx(tx *bolt.Tx, packageID int64, files []*FileEntry) error {
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
