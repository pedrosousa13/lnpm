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

// TestHashFilesFramesFields pins the fix for #453: the per-file fields go into
// the hash framed, so no filename can absorb the boundary between one file's
// record and the next.
//
// The construction is the one ADR-0007 reproduces, and it needs no
// cryptanalysis - the two sets below differ in file count and are made only of
// ordinary readable files with legal names. Unframed, the first set streams
// "zfoo" + "858b1150d7814175" + "644" + "zzbar" + "0123456789abcdef" + "755",
// which is byte for byte what the single crafted filename in the second streams.
func TestHashFilesFramesFields(t *testing.T) {
	twoFiles := []*FileInfo{
		{RelPath: "zfoo", ContentHash: "858b1150d7814175", Mode: 0644},
		{RelPath: "zzbar", ContentHash: "0123456789abcdef", Mode: 0755},
	}
	oneFile := []*FileInfo{
		{RelPath: "zfoo858b1150d7814175644zzbar", ContentHash: "0123456789abcdef", Mode: 0755},
	}

	if HashFiles(twoFiles) == HashFiles(oneFile) {
		t.Errorf("a filename absorbed a record boundary: both sets hash to %s", HashFiles(twoFiles))
	}
}
