// Package ui renders lnpm's user-facing notices: the status markers, section
// headers and horizontal rules that commands print, and the decision about
// whether to draw them as glyphs or as plain ASCII.
//
// It exists so there is one spelling of a notice everywhere. The helpers used
// to be unexported in internal/cli/output.go, and internal/cli imports
// internal/pack, so internal/pack could not reach them and spelled its warnings
// its own way. Exporting them where they were would have made importing them
// back an import cycle. This package depends on nothing inside the module, so
// both internal/cli and internal/pack can import it and neither creates one.
//
// The better long-term layering is the other one: internal/pack returns its
// notices and lets the CLI render them. A library that writes to stdout decides
// for its callers where the text goes and whether it appears at all, which is
// not a library's decision to make. That was not done here because Pack's
// signature is (*PackageJSON, []*FileInfo, error) and carrying notices out
// would change it and every one of its callers - a larger change than the
// mismatched warning prefix warranted. If pack's output grows past notices, or
// a caller needs to suppress or redirect it, that is the direction to take.
package ui

import (
	"os"
	"strings"
)

// IsTTY reports whether the given file is connected to an interactive
// terminal (a character device). This is used to decide whether decorative
// glyphs and color are appropriate.
func IsTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// decorate reports whether decorative output (emoji, box-drawing, color) should
// be emitted. It is true only when stdout is a real terminal and NO_COLOR is
// not set, so piped or scripted output stays plain ASCII.
//
// Both inputs are process-wide - the environment and os.Stdout - so the answer
// does not depend on which package asked.
func decorate() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return IsTTY(os.Stdout)
}

// icon returns the decorative glyph when decorating, otherwise a plain ASCII
// fallback so piped/scripted output is not polluted with emoji.
func icon(glyph, plain string) string {
	if decorate() {
		return glyph
	}
	return plain
}

// Status markers used across commands. They render as glyphs on a TTY and as
// plain ASCII otherwise.
func IconOK() string   { return icon("✓", "OK") }
func IconFail() string { return icon("✗", "x") }
func IconWarn() string { return icon("⚠", "!") }
func IconTip() string  { return icon("💡", "tip:") }

// Header renders a decorative section header. When not decorating, the emoji
// prefix is dropped so the label is plain ASCII.
func Header(emoji, label string) string {
	if decorate() {
		return emoji + " " + label
	}
	return label
}

// HRule returns a horizontal rule of the given width using box-drawing on a
// TTY and ASCII dashes otherwise.
func HRule(width int) string {
	ch := "-"
	if decorate() {
		ch = "─"
	}
	return strings.Repeat(ch, width)
}
