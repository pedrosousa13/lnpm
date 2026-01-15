package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	bolt "go.etcd.io/bbolt"
)

// debugCmd provides debugging utilities
var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Debugging utilities",
	Long:  `Access debugging commands for troubleshooting and inspection.`,
}

// debugDbCmd inspects the database contents
var debugDbCmd = &cobra.Command{
	Use:   "db",
	Short: "Inspect database contents",
	Long:  `Print database statistics and bucket contents for debugging.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return DebugDatabase()
	},
}

// DebugDatabase prints database information
func DebugDatabase() error {
	// Get database path
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	storePath := filepath.Join(home, ".lnpm")
	dbPath := filepath.Join(storePath, "lnpm.db")

	// Check if database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Printf("Database not found at: %s\n", dbPath)
		return nil
	}

	// Open database
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	// Print database info
	fmt.Printf("📊 Database Information\n")
	fmt.Printf("Path: %s\n", dbPath)
	fileInfo, _ := os.Stat(dbPath)
	if fileInfo != nil {
		fmt.Printf("Size: %.2f MB\n", float64(fileInfo.Size())/1024/1024)
	}
	fmt.Println()

	// List all buckets and their contents
	err = db.View(func(tx *bolt.Tx) error {
		buckets := []string{
			"packages",
			"packages_by_name",
			"projects",
			"projects_by_path",
			"links",
			"links_by_package",
			"links_by_project",
			"files",
			"meta",
		}

		for _, bucketName := range buckets {
			b := tx.Bucket([]byte(bucketName))
			if b == nil {
				continue
			}

			count := 0
			b.ForEach(func(k, v []byte) error {
				count++
				return nil
			})

			if count > 0 {
				fmt.Printf("📦 %s (%d items)\n", bucketName, count)

				// Show first few items
				bc := b.Cursor()
				itemCount := 0
				for k, v := bc.First(); k != nil && itemCount < 5; k, v = bc.Next() {
					if len(v) > 100 {
						fmt.Printf("  %s: %s... (%d bytes)\n", string(k), string(v[:100]), len(v))
					} else {
						fmt.Printf("  %s: %s\n", string(k), string(v))
					}
					itemCount++
				}
				if count > 5 {
					fmt.Printf("  ... and %d more items\n", count-5)
				}
				fmt.Println()
			}
		}

		return nil
	})

	return err
}

// debugSizeCmd shows database usage
var debugSizeCmd = &cobra.Command{
	Use:   "size",
	Short: "Show database size",
	Long:  `Display database file size and bucket breakdown.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}

		storePath := filepath.Join(home, ".lnpm")
		dbPath := filepath.Join(storePath, "lnpm.db")

		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			fmt.Printf("Database not found at: %s\n", dbPath)
			return nil
		}

		fileInfo, _ := os.Stat(dbPath)
		fmt.Printf("📊 Database Size: %.2f MB\n", float64(fileInfo.Size())/1024/1024)

		// Open database
		db, err := bolt.Open(dbPath, 0600, &bolt.Options{ReadOnly: true})
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer db.Close()

		err = db.View(func(tx *bolt.Tx) error {
			buckets := []string{
				"packages",
				"packages_by_name",
				"projects",
				"projects_by_path",
				"links",
				"links_by_package",
				"links_by_project",
				"files",
				"meta",
			}

			totalSize := int64(0)
			for _, bucketName := range buckets {
				b := tx.Bucket([]byte(bucketName))
				if b == nil {
					continue
				}

				// Estimate bucket size
				bucketSize := int64(0)
				b.ForEach(func(k, v []byte) error {
					bucketSize += int64(len(k) + len(v))
					return nil
				})

				if bucketSize > 0 {
					fmt.Printf("  %s: %.2f KB\n", bucketName, float64(bucketSize)/1024)
					totalSize += bucketSize
				}
			}

			fmt.Printf("\nEstimated data size: %.2f KB\n", float64(totalSize)/1024)
			return nil
		})

		return err
	},
}

func init() {
	debugCmd.AddCommand(debugDbCmd)
	debugCmd.AddCommand(debugSizeCmd)
}