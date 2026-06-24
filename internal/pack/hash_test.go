package pack

import "testing"

func TestHashFilesOrderIndependent(t *testing.T) {
	a := []*FileInfo{
		{RelPath: "a.js", ContentHash: "1", Mode: 0644},
		{RelPath: "b.js", ContentHash: "2", Mode: 0644},
	}
	b := []*FileInfo{
		{RelPath: "b.js", ContentHash: "2", Mode: 0644},
		{RelPath: "a.js", ContentHash: "1", Mode: 0644},
	}
	if HashFiles(a) != HashFiles(b) {
		t.Error("HashFiles must be independent of file order")
	}
}

func TestHashFilesModeSensitive(t *testing.T) {
	a := []*FileInfo{{RelPath: "cli.js", ContentHash: "1", Mode: 0644}}
	b := []*FileInfo{{RelPath: "cli.js", ContentHash: "1", Mode: 0755}}
	if HashFiles(a) == HashFiles(b) {
		t.Error("HashFiles must change when a file's mode changes (e.g. chmod +x)")
	}
}
