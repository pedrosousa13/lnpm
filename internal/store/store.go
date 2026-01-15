package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// Store manages the package store at ~/.lnpm/store
type Store struct {
	basePath string
}

// New creates a new Store instance
func New() (*Store, error) {
	basePath, err := getStorePath()
	if err != nil {
		return nil, err
	}

	storePath := filepath.Join(basePath, "store")
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	return &Store{basePath: storePath}, nil
}

// getStorePath returns the lnpm store root path
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

// PackagePath returns the path to a package in the store
func (s *Store) PackagePath(name, hash string) string {
	return filepath.Join(s.basePath, name, hash)
}

// Exists checks if a package with the given hash exists
func (s *Store) Exists(name, hash string) bool {
	path := s.PackagePath(name, hash)
	_, err := os.Stat(path)
	return err == nil
}

// Store copies files to the store
func (s *Store) Store(name, hash string, files []*pack.FileInfo, sourceDir string) (string, error) {
	destPath := s.PackagePath(name, hash)
	debug.Logf("store: storing %s hash=%s files=%d dest=%s", name, hash[:8], len(files), destPath)

	// Remove existing if present (for updates)
	if err := os.RemoveAll(destPath); err != nil {
		return "", fmt.Errorf("failed to clean existing store path: %w", err)
	}

	// Create destination directory
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create store directory: %w", err)
	}

	debug.Log("store: copying files")
	// Copy each file
	for _, f := range files {
		destFile := filepath.Join(destPath, f.RelPath)

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
			return "", fmt.Errorf("failed to create directory for %s: %w", f.RelPath, err)
		}

		// Copy file
		if err := copyFile(f.Path, destFile, f.Mode); err != nil {
			return "", fmt.Errorf("failed to copy %s: %w", f.RelPath, err)
		}
	}

	return destPath, nil
}

// Remove removes a package from the store
func (s *Store) Remove(name, hash string) error {
	path := s.PackagePath(name, hash)
	return os.RemoveAll(path)
}

// List returns all packages in the store
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var packages []string
	for _, entry := range entries {
		if entry.IsDir() {
			packages = append(packages, entry.Name())
		}
	}
	return packages, nil
}

// ListVersions returns all versions (hashes) of a package
func (s *Store) ListVersions(name string) ([]string, error) {
	packagePath := filepath.Join(s.basePath, name)
	entries, err := os.ReadDir(packagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var versions []string
	for _, entry := range entries {
		if entry.IsDir() {
			versions = append(versions, entry.Name())
		}
	}
	return versions, nil
}

// GetFiles returns all files in a stored package
func (s *Store) GetFiles(name, hash string) ([]*pack.FileInfo, error) {
	storePath := s.PackagePath(name, hash)

	var files []*pack.FileInfo
	err := filepath.Walk(storePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(storePath, path)
		if err != nil {
			return err
		}

		files = append(files, &pack.FileInfo{
			Path:    path,
			RelPath: filepath.ToSlash(relPath),
			Size:    info.Size(),
			Mode:    info.Mode(),
		})

		return nil
	})

	return files, err
}

// copyFile copies a file preserving permissions
func copyFile(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return dstFile.Sync()
}
