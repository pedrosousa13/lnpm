package cli

import (
	"os"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/fsutil"
)

// projectDirState is what a scan concluded about a project directory.
type projectDirState int

const (
	// projectLive means the link is kept. Either the directory stat'd, or the
	// stat failed for a reason that establishes nothing about whether it is
	// there.
	projectLive projectDirState = iota
	// projectGone means gc may drop the link row and collect. It is reached for
	// two different reasons, and they are not equally strong - a reader
	// switching on this must not assume the first:
	//
	//   - Positive evidence of deletion: the filesystem that held the project
	//     is mounted where it should be, and the project is not on it.
	//   - No comparison was possible, so this is the pre-#335 behaviour. Either
	//     no device was ever recorded for the project, or none can be read for
	//     the path now. Neither is evidence of anything; both fall back rather
	//     than invent a conclusion, because refusing instead would stop gc
	//     collecting anything at all on databases written before the device
	//     field existed.
	//
	// The distinction is deliberately not surfaced as two states. gc does the
	// same thing in both cases, and a state it cannot act on differently would
	// only invite a caller to act on it wrongly.
	projectGone
	// projectUnreachable means the directory does not stat, and the filesystem
	// that held it is not mounted there - so no conclusion is available. gc
	// declines to judge and reports the link as skipped.
	projectUnreachable
)

func (s projectDirState) String() string {
	switch s {
	case projectGone:
		return "projectGone"
	case projectUnreachable:
		return "projectUnreachable"
	default:
		return "projectLive"
	}
}

// classifyProjectDir decides whether a project directory was deleted or is
// merely out of reach, and returns the device it was observed on when it could
// be stat'd - zero otherwise, so a caller can re-record it.
//
// # Why a device comparison, and not something simpler
//
// An unmounted drive and a deleted directory are the same thing to stat: both
// give ENOENT. Three simpler tests were measured against a real tmpfs unmounted
// in a user+mount namespace, and none of them separates the two:
//
//   - Ancestor reachability. A drive's mount point is an ordinary directory on
//     the parent filesystem and stays there when the drive goes away, so the
//     parent is reachable in both cases. It changed no verdict in any state
//     tested. It only fires when an ancestor is itself deleted, which is a
//     stronger case for orphaning rather than a weaker one.
//   - Mount-point detection, comparing a directory's device against its
//     parent's. This answers "is something mounted here now", and after an
//     unmount the answer is no: the mount point reverts to its parent's device
//     and is indistinguishable from any empty directory. Worse, it is inverted -
//     it would keep links live on drives that are mounted, where the truth is
//     visible, and drop them on drives that are not.
//   - A marker inside the project. Nothing under the project path is readable
//     when the path itself is gone, and lnpm records nothing about a project
//     outside it.
//
// What is left is the filesystem's own identity, which has to have been written
// down while the project was reachable. Hence db.Project.Device.
//
// # Where this gives a wrong answer
//
// It is a heuristic. Five gaps, all of which should stay documented rather than
// be implied away:
//
//   - Nothing is protected until a device has been recorded once while the
//     project was reachable. Upgrading lnpm with a drive already unmounted and
//     running gc --yes still loses the entry; the protection arrives one
//     reachable run late.
//   - An anonymous device number is not stable across a remount. Measured on
//     Linux 6.12: a tmpfs unmounted and remounted after two other mounts had
//     taken slots moved from 163 to 214. tmpfs, btrfs, overlayfs, FUSE and NFS
//     all get anonymous numbers - and NFS is half the scenario #335 was filed
//     for. A genuinely deleted project on a remounted export is therefore
//     declined rather than collected: the safe direction, but it leaks space,
//     which is why the skipped count is reported and why gc re-stamps the
//     device on every successful stat.
//   - A recycled number can still misclassify. Unmount drive A, mount drive B
//     at the same path, and if B is assigned A's old anonymous number the
//     project reads as deleted.
//   - Windows compares volume serial numbers instead, which are stable across
//     unplug and replug but reassigned by a reformat. A filesystem reporting a
//     serial of zero gets no protection at all, because zero is how this
//     encodes "unknown".
//   - A mount that shares a device with what it is mounted on is not protected,
//     because there is nothing to compare. A bind mount of a directory on the
//     same filesystem is the case: measured end to end, both the mount and its
//     parent reported device 10302, and gc collected the package across the
//     unmount exactly as it did before this fix. A real external drive or an NFS
//     export is a separate filesystem and does not have this problem - the same
//     scenario over tmpfs reported 237 against a parent of 66306 and was kept.
//     It is called out because it makes bind mounts a misleading way to test
//     this code: they look like the bug reproducing after it was fixed.
func classifyProjectDir(path string, recordedDevice uint64) (projectDirState, uint64) {
	if _, err := os.Stat(path); err == nil {
		// This reads the path twice, and the second read is not avoidable by
		// passing the FileInfo along. On Windows the device is the volume
		// serial number, which only arrives through an open handle - a FileInfo
		// carries no volume identity at all - so there is no single call that
		// answers both "is it there" and "what is it on" across platforms.
		// Reordering to ask for the device first would save the extra call on
		// live projects, at the cost of making the existence question the
		// secondary one in the code that exists to answer it. The scan already
		// walks every project directory, so the clarity is worth more here.
		return projectLive, fsutil.DeviceIDOfPath(path)
	} else if !os.IsNotExist(err) {
		// A permission or I/O failure says nothing about whether the directory
		// is there, so it cannot support deleting anything. This fail-safe
		// predates #335 and is deliberately kept.
		return projectLive, 0
	}

	// Every record written before this field existed holds zero, and so does
	// any path on a platform that reports no device. Unknown has to mean "judge
	// it the way lnpm always did" and not "skip": skipping sounds safer and
	// would stop gc collecting anything at all on every install that upgrades,
	// silently, which is the acceptance criterion this fix must not break.
	if recordedDevice == 0 {
		return projectGone, 0
	}

	current := fsutil.DeviceIDOfPath(nearestExistingAncestor(path))
	if current == 0 {
		// Nothing along the path can be read either. No comparison is possible,
		// so fall back to the pre-#335 behaviour rather than invent one.
		return projectGone, 0
	}
	if current != recordedDevice {
		return projectUnreachable, 0
	}
	return projectGone, 0
}

// nearestExistingAncestor returns the deepest ancestor of path that stats.
//
// It climbs rather than stopping at the immediate parent because deleting a tree
// of projects takes their shared parent with it, and there would be nothing at
// the parent to compare a device against. The climb terminates at the root,
// whose parent is itself.
func nearestExistingAncestor(path string) string {
	for cur := filepath.Dir(filepath.Clean(path)); ; {
		if _, err := os.Stat(cur); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return cur
		}
		cur = parent
	}
}
