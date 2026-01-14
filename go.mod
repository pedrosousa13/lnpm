module github.com/user/lnpm

go 1.22

require (
	github.com/bmatcuk/doublestar/v4 v4.6.1
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/spf13/cobra v1.8.1
	go.etcd.io/bbolt v1.3.8
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/sys v0.4.0 // indirect
)

// Dependencies to add when network is available:
// github.com/fatih/color v1.17.0             // Terminal colors
// github.com/charmbracelet/lipgloss v0.13.0  // Rich terminal UI
// github.com/fsnotify/fsnotify v1.7.0        // File watching
// github.com/spf13/viper v1.19.0             // Configuration
// modernc.org/sqlite v1.33.1                 // SQLite (pure Go) - swap db/db.go for SQLite version
