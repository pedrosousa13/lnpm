package cli

import (
	"strings"
	"testing"
)

func TestShouldProceed(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		yes         bool
		want        confirmDecision
		wantMsg     bool
	}{
		{
			name:        "non-interactive without yes aborts with a message",
			interactive: false,
			yes:         false,
			want:        decisionAbort,
			wantMsg:     true,
		},
		{
			name:        "non-interactive with yes proceeds",
			interactive: false,
			yes:         true,
			want:        decisionProceed,
		},
		{
			name:        "interactive without yes asks the user",
			interactive: true,
			yes:         false,
			want:        decisionAsk,
		},
		{
			name:        "interactive with yes proceeds without asking",
			interactive: true,
			yes:         true,
			want:        decisionProceed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := shouldProceed(tc.interactive, tc.yes)
			if got != tc.want {
				t.Fatalf("shouldProceed(%t, %t) = %v, want %v", tc.interactive, tc.yes, got, tc.want)
			}
			if tc.wantMsg {
				if !strings.Contains(msg, "--yes") {
					t.Fatalf("shouldProceed(%t, %t) msg = %q, want it to mention --yes", tc.interactive, tc.yes, msg)
				}
			} else if msg != "" {
				t.Fatalf("shouldProceed(%t, %t) msg = %q, want empty", tc.interactive, tc.yes, msg)
			}
		})
	}
}
