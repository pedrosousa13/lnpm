package debug

import (
	"fmt"
	"os"
	"time"
)

var enabled bool

// Init initializes debug mode
func Init(debug bool) {
	enabled = debug || os.Getenv("LNPM_DEBUG") != ""
	if enabled {
		Log("debug mode enabled")
	}
}

// Enabled returns whether debug mode is active
func Enabled() bool {
	return enabled
}

// Log prints a debug message with timestamp
func Log(msg string, args ...any) {
	if !enabled {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "[DEBUG %s] %s %v\n", ts, msg, args)
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG %s] %s\n", ts, msg)
	}
}

// Logf prints a formatted debug message
func Logf(format string, args ...any) {
	if !enabled {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stderr, "[DEBUG %s] %s\n", ts, fmt.Sprintf(format, args...))
}
