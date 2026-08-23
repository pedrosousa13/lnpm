package fsutil

import (
	"errors"
	"fmt"
	"os"
)

// MaxYAMLBytes is the largest YAML document lnpm will read before parsing it.
//
// It exists to bound parse cost, not memory. gopkg.in/yaml.v3's cost is
// superlinear in the input, and the sweep on #323 measured it going up faster
// than the file does:
//
//	entries     size    elapsed
//	  5,000    1.1MB      165ms
//	 10,000    2.2MB      806ms
//	 20,000    4.5MB      2.69s
//	 40,000    9.0MB      25.1s
//	100,000     21MB    3m35.5s, 259MiB allocated
//
// Doubling 20,000 entries to 40,000 costs 9.3x the time (25.1/2.69). Per byte
// that is 0.598 s/MB against 2.79 s/MB, so the rate itself rises 4.6x across
// those two rows. That cost is inside the YAML library rather than in lnpm's own
// structs, and go-yaml has been archived upstream since 2025, so a cap at read
// time is the mitigation available.
//
// The 4 MiB figure comes from those rows rather than from roundness. They put a
// lock entry at about 225 bytes (4.5MB/20,000, and 9.0MB/40,000), so 4 MiB of
// lock-shaped content is roughly 18,600 entries - between the 10,000-entry row
// at 806ms and the 20,000-entry row at 2.69s.
//
// That is a typical-case bound and not a worst case, which matters to anyone who
// later leans on it. The cap counts bytes; yaml.v3's cost tracks node count, and
// 225 bytes per entry is what *lock-shaped* content costs. Measured while
// writing this, best of five on one machine: 4 MiB of lock-shaped YAML is
// 254,000 nodes and unmarshals in 251ms, while 4 MiB written for node count
// instead - a flow sequence of one-byte scalars - is 2.1 million nodes and
// unmarshals in 1.14s. Same byte budget, 8x the nodes, 4.6x the time. No search
// was made for the worst shape, so read this cap as bounding the documents lnpm
// actually reads rather than every 4 MiB document.
//
// Nothing legitimate comes near it: lnpm.lock records the packages a project has
// *linked*, not a resolved dependency graph, so real files run to tens of
// entries.
//
// One constant covers the lock file and pnpm-workspace.yaml both. A workspace
// config is far smaller than a lock file, so a tighter second cap would refuse
// things this one accepts - 64 KiB on the config would turn away a 3 MiB
// pnpm-workspace.yaml that 4 MiB lets through. That is a real difference; it is
// just not worth a second number. Neither figure is a size a legitimate
// workspace config reaches, and a second constant would need its own
// measurements to defend and its own reason when someone later asks why the two
// disagree.
//
// It lives in fsutil rather than beside a caller because pkg/lockfile and
// internal/workspace both need it and neither should depend on the other. It is
// a property of yaml.v3's cost curve, not of the filesystem, which is why the
// measurements are written down here instead of being left for a reader to
// reconstruct.
const MaxYAMLBytes = 4 << 20

// ErrFileTooLarge is returned by ReadFileCapped when a file is over the limit.
//
// Nothing in production branches on it - the callers pass the refusal straight
// up. The tests do, and that is what it is for. #323 asks that moving the size
// check to after the unmarshal turn the refusal tests red, and the fixtures are
// oversized *and* invalid YAML, so the only thing that tells the two placements
// apart is whether what comes back is this error or a parse error. Matching on
// the message instead would pin the wording rather than the behaviour.
var ErrFileTooLarge = errors.New("file is over the size limit")

// ReadFileCapped reads path, refusing anything that is not a regular file and
// any regular file larger than max bytes.
//
// The size is taken from os.Stat before any read, which is what makes the
// refusal free: an oversized file is never read into memory and never reaches a
// parser, so the cost the cap exists to bound is never paid. It is also what
// lets a test build the oversized case with os.Truncate instead of writing
// megabytes.
//
// The regular-file check is what makes the size check mean anything. os.Stat
// reports Size() == 0 for a FIFO, a device node and their like, so without the
// check such a path sails past the comparison and reaches os.ReadFile
// unbounded - and a FIFO with no writer blocks there rather than yielding a
// small file, which is the opposite of what a size of 0 suggested. A zero size
// from a non-regular file is not the same fact as a zero size from a small one.
//
// What it is not is a hard bound. Stat and the read are two calls, so a writer
// growing the file in between hands back more than max, and lnpm parses it. That
// window is accepted rather than closed: reading through an io.LimitReader would
// make the bound absolute, but it would give up the free refusal above, opening
// and reading an oversized file up to the limit instead of turning it away on
// its stat. It would cost no more on legitimate files, which are already read in
// full here. The threat #323 describes is a repo that ships an oversized file,
// not a writer racing the read, and this closes that one.
//
// A missing file is reported as the *fs.PathError os.Stat produced, so
// os.IsNotExist and errors.Is(err, fs.ErrNotExist) still recognise it -
// pkg/lockfile turns that case into "no lock file here" and has to keep being
// able to. The refusal names the file, its size and the limit, because a user
// who hits it has to decide whether the file is corrupt or the limit is wrong,
// and cannot do either without all three.
func ReadFileCapped(path string, max int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > max {
		return nil, fmt.Errorf("%s is %d bytes, over the %d-byte limit: %w", path, info.Size(), max, ErrFileTooLarge)
	}
	return os.ReadFile(path)
}
