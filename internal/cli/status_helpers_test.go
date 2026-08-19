package cli

import (
	"testing"
	"time"
	"unicode/utf8"
)

// TestTruncate pins the column-fitting helper the status tables rely on: a
// truncated value must never exceed its column, must end in an ellipsis when
// something was dropped, and must never split a multibyte character (the
// tables print package names and paths, which are not ASCII-only).
func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"shorter than the column is unchanged", "hello", 10, "hello"},
		{"exactly the column width is unchanged", "hello", 5, "hello"},
		{"empty string is unchanged", "", 5, ""},
		{"longer than the column ends in an ellipsis", "hello world", 8, "hello..."},
		{"one over the column drops three characters", "hello", 4, "h..."},
		{"a column of 4 is the narrowest that keeps a character", "hello world", 4, "h..."},
		{"a column of 3 is a hard cut with no ellipsis", "hello", 3, "hel"},
		{"a column of 1 is a hard cut", "hello", 1, "h"},
		{"a column of 0 drops everything", "hello", 0, ""},
		{"multibyte shorter than the column is unchanged", "héllo wörld", 11, "héllo wörld"},
		{"multibyte is cut on rune boundaries", "héllo wörld", 8, "héllo..."},
		{"multibyte counts runes, not bytes", "hé", 2, "hé"},
		{"multibyte hard cut stays on a rune boundary", "héllo", 2, "hé"},
		{"the ellipsis is not counted in bytes", "wörld wörld", 6, "wör..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.maxLen)

			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncate(%q, %d) = %q, which is not valid UTF-8", tt.s, tt.maxLen, got)
			}
			if n := utf8.RuneCountInString(got); n > tt.maxLen {
				t.Errorf("truncate(%q, %d) = %q, which is %d runes wide and overflows the column",
					tt.s, tt.maxLen, got, n)
			}
		})
	}
}

// TestFormatTimeAgo pins the humanized age shown in the status tables and the
// list output, including the singular forms and each unit boundary. Every case
// is expressed as a duration before the same "now", so the test is independent
// of when it runs, and the past-a-week fallback is compared against the same
// time formatted by the test rather than a fixed date string.
func TestFormatTimeAgo(t *testing.T) {
	type testCase struct {
		name string
		ago  time.Duration
		want string
		// wantDate marks the past-a-week cases, whose expectation is the
		// timestamp itself formatted as a date. Deriving it in the runner from
		// the very instant passed to the helper keeps the test correct whatever
		// day it runs on, and keeps the subtest names static.
		wantDate bool
	}

	const day = 24 * time.Hour

	tests := []testCase{
		{name: "under a minute", ago: 30 * time.Second, want: "just now"},
		{name: "one second under a minute", ago: 59 * time.Second, want: "just now"},
		{name: "exactly a minute is singular", ago: time.Minute, want: "1 minute ago"},
		{name: "several minutes are plural", ago: 5 * time.Minute, want: "5 minutes ago"},
		{name: "one minute under an hour", ago: 59 * time.Minute, want: "59 minutes ago"},
		{name: "exactly an hour is singular", ago: time.Hour, want: "1 hour ago"},
		{name: "several hours are plural", ago: 3 * time.Hour, want: "3 hours ago"},
		{name: "one hour under a day", ago: 23 * time.Hour, want: "23 hours ago"},
		{name: "exactly a day is singular", ago: day, want: "1 day ago"},
		{name: "several days are plural", ago: 3 * day, want: "3 days ago"},
		{name: "one hour under a week", ago: 7*day - time.Hour, want: "6 days ago"},
		{name: "exactly a week falls back to a date", ago: 7 * day, wantDate: true},
		{name: "ten days falls back to a date", ago: 10 * day, wantDate: true},
		{name: "over a year falls back to a date", ago: 400 * day, wantDate: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Built here rather than from a "now" shared by the whole table:
			// the 59-second case sits one second below a boundary, so any
			// elapsed time between building the table and running the case
			// could flip it to "1 minute ago".
			ts := time.Now().Add(-tt.ago)

			want := tt.want
			if tt.wantDate {
				want = ts.Format("Jan 2, 2006")
			}

			if got := formatTimeAgo(ts); got != want {
				t.Errorf("formatTimeAgo(%v ago) = %q, want %q", tt.ago, got, want)
			}
		})
	}
}

// TestFormatTimeAgoFutureTime pins what the helper does with a timestamp in the
// future: time.Since is negative, which falls into the first branch, so it
// reads as "just now" rather than producing a negative count.
func TestFormatTimeAgoFutureTime(t *testing.T) {
	if got := formatTimeAgo(time.Now().Add(time.Hour)); got != "just now" {
		t.Errorf("formatTimeAgo(an hour from now) = %q, want %q", got, "just now")
	}
}
