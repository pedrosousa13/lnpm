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
// than the file does: 5,000 lock entries (1.1MB) took 165ms, 20,000 (4.5MB) took
// 2.69s, 40,000 (9.0MB) took 25.1s - 4.6x the work for 2x the input - and
// 100,000 (21MB) took 3m35s allocating 259MiB. That cost is inside the YAML
// library rather than in lnpm's own structs, and go-yaml has been archived
// upstream since 2025, so a cap at read time is the mitigation available.
//
// The 4 MiB figure comes from those rows rather than from roundness. They put a
// lock entry at about 225 bytes (4.5MB/20,000, and 9.0MB/40,000), so 4 MiB is
// roughly 18,600 entries - between the 10,000-entry row at 806ms and the
// 20,000-entry row at 2.69s, which bounds a worst-case parse to a few seconds.
// Nothing legitimate comes near it: lnpm.lock records the packages a project has
// *linked*, not a resolved dependency graph, so real files run to tens of
// entries.
//
// One constant covers the lock file and pnpm-workspace.yaml both. A workspace
// config is far smaller than a lock file, but a second number would need its own
// justification and would bound nothing the first does not.
const MaxYAMLBytes = 4 << 20

// ErrFileTooLarge is returned by ReadFileCapped when a file is over the limit.
// Callers match on it to tell a refusal apart from a file that would not read.
var ErrFileTooLarge = errors.New("file is over the size limit")

// ReadFileCapped reads path, refusing files larger than max bytes.
//
// The size is taken from os.Stat before any read, which is what makes the
// refusal free: an oversized file is never read into memory and never reaches a
// parser, so the cost the cap exists to bound is never paid. It is also what
// lets a test build the oversized case with os.Truncate instead of writing
// megabytes.
//
// What it is not is a hard bound. Stat and the read are two calls, so a writer
// growing the file in between hands back more than max, and lnpm parses it. That
// window is accepted rather than closed: reading through an io.LimitReader would
// make the bound absolute and would cost a full read of every legitimate file to
// do it, losing the free refusal above. The threat #323 describes is a repo that
// ships an oversized file, not a writer racing the read, and this closes that
// one.
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
	if info.Size() > max {
		return nil, fmt.Errorf("%s is %d bytes, over the %d-byte limit: %w", path, info.Size(), max, ErrFileTooLarge)
	}
	return os.ReadFile(path)
}
