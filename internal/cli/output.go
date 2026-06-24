package cli

import (
	"bufio"
	"os"
	"strings"
)

// isTTY reports whether the given file is connected to an interactive
// terminal (a character device). This is used to decide whether decorative
// glyphs and color are appropriate.
func isTTY(f *os.File) bool {
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
func decorate() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return isTTY(os.Stdout)
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
func iconOK() string   { return icon("✓", "OK") }
func iconFail() string { return icon("✗", "x") }
func iconWarn() string { return icon("⚠", "!") }
func iconTip() string  { return icon("💡", "tip:") }

// header renders a decorative section header. When not decorating, the emoji
// prefix is dropped so the label is plain ASCII.
func header(emoji, label string) string {
	if decorate() {
		return emoji + " " + label
	}
	return label
}

// hrule returns a horizontal rule of the given width using box-drawing on a
// TTY and ASCII dashes otherwise.
func hrule(width int) string {
	ch := "-"
	if decorate() {
		ch = "─"
	}
	return strings.Repeat(ch, width)
}

// confirm prompts the user for a yes/no answer, defaulting to No. It reads a
// single line from stdin. The prompt is only shown when the session is fully
// interactive (both stdin and stdout are terminals). For scripts, pipes,
// redirected output, or tests it does not prompt and returns proceed=true so
// non-interactive callers are not blocked waiting for input.
func confirm(prompt string) bool {
	if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		return true
	}
	os.Stdout.WriteString(prompt + " [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
