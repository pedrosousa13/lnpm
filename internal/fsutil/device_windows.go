//go:build windows

package fsutil

import (
	"os"

	"golang.org/x/sys/windows"
)

// DeviceID returns the device ID for the given file info.
//
// It is always 0 on Windows, and that is a limit of the input rather than a
// decision: os.FileInfo.Sys() yields a *syscall.Win32FileAttributeData, which
// carries no volume identity at all. The number exists but only reachable
// through an open handle, which a FileInfo does not carry - so DeviceIDOfPath
// is the form that answers on this platform, and callers needing a real answer
// everywhere must use it.
//
// link.go's determineLinkType still uses this form and still gets 0 here. That
// is pre-existing and deliberate: it short-circuits to HardLink on Windows
// before consulting any device, and tries the link rather than predicting it.
func DeviceID(info os.FileInfo) uint64 {
	return 0
}

// DeviceIDOfPath returns the serial number of the volume the path is on, or 0
// if that cannot be established.
//
// PROVENANCE. The design below was read from the golang.org/x/sys/windows source
// and the Win32 documentation, not run: this repo's development happens on Linux.
// CI has since executed it, which upgrades part of it from argued to established.
//
//   - RUN, on the Windows CI runner for PR #383: TestDeviceIDOfPath* assert that
//     this returns a non-zero volume serial for an existing file and for an
//     existing directory, the same value for two paths on one volume, and zero
//     for a path that does not exist. That run reported failures in internal/cli
//     and internal/db and none in internal/fsutil, so those three passed. The
//     directory case passing is what establishes the CreateFile flags: without
//     FILE_FLAG_BACKUP_SEMANTICS it would have returned zero and gone red.
//     (Established from which tests CI reported failing, not from reading the
//     runner's log line by line.)
//   - STILL READ, NOT RUN: that a serial survives unplug and replug of the same
//     volume, and that a reformat reassigns it. CI mounts nothing, so no test
//     here touches either. Treat those two as argued.
//
// Per docs/agents/verification-discipline.md, which half is which is the point
// of stating it.
//
// The Windows analogue of a Unix st_dev is dwVolumeSerialNumber, which arrives
// only via GetFileInformationByHandle - so unlike the Unix side this has to open
// the file rather than stat it. Three details of that open are load-bearing:
//
//   - FILE_FLAG_BACKUP_SEMANTICS. CreateFile is documented to require it to
//     obtain a handle to a directory, and every path gc asks about is a
//     directory. A build missing this flag would return 0 for exactly the
//     inputs that matter and degrade gc to its pre-fix behaviour in silence.
//   - Zero desired access. Metadata by handle is documented to need no read or
//     write right, so this should succeed on files the caller may not read and
//     not fail on a project directory with restrictive permissions.
//   - Full FILE_SHARE_*. Anything less would fail on a directory another process
//     holds open, which on a machine running an editor is most of them.
//
// Zero is returned when the volume serial cannot be read - a path that does not
// exist, an open that fails, or a call that fails - and gc reads that as
// "unknown", which sends it back to its pre-#335 behaviour rather than to a
// wrong comparison.
//
// Whether any filesystem reports a *successful* serial of 0 is not something
// this repo has established, so no claim is made either way, and the code does
// not depend on one: the zero handling above is required by the error paths
// regardless. It is worth being exact because the assertion and the test have
// to agree - TestDeviceIDOfPathIsNonZeroAndSharedWithinOneFilesystem requires a
// non-zero device on every platform, so a filesystem that did report 0 would
// turn CI red rather than silently disable the protection. That is the intended
// direction: a loud failure on a runner is recoverable, a quiet degradation to
// pre-fix gc behaviour on every Windows user's machine is not.
//
// The serial is stable across unplug and replug of the same volume, but it is
// not a UUID: it is reassigned by a reformat, and can collide between volumes.
// gc only ever compares it against a value recorded from the same path earlier,
// which is what keeps a collision from mattering in practice rather than any
// property of the number.
func DeviceIDOfPath(path string) uint64 {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	h, err := windows.CreateFile(
		p,
		0, // metadata only: no read or write access is needed
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS, // required to open a directory
		0,
	)
	if err != nil {
		return 0
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return 0
	}
	return uint64(info.VolumeSerialNumber)
}
