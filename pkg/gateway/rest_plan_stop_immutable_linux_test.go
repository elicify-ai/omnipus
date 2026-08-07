//go:build linux

// rest_plan_stop_immutable_linux_test.go — Linux implementation of
// blockNewFilesInDir, the shared test helper TestPlanStop_PartialFailureSurfacesAsServerError
// (rest_plan_task_restart_test.go) uses to force a real store-level I/O
// failure on a member task's cancel-write. See that test's doc comment for
// the two approaches that were tried and rejected before this one:
//
//  1. chmod 0o500 on the tasks dir alone — works for an unprivileged local
//     dev run, but the ci-omnipus CI worker runs tests AS ROOT, where
//     CAP_DAC_OVERRIDE makes a permission-stripped directory block nothing;
//     the write silently succeeds and the test falsely observes 200.
//  2. replacing the member's task file with a non-empty directory — this
//     DOES block the write everywhere (EISDIR/ENOTEMPTY), but it also
//     breaks taskStore.List/ListIDs, which silently skip non-regular-file
//     directory entries: the doomed member vanishes from StopPlan's own
//     fan-out enumeration entirely, so the call trivially returns a clean
//     200 — masking the exact partial-failure path under test.
//
// This file's approach handles both privilege levels correctly: chmod for
// the unprivileged case (unchanged from approach 1), and the Linux
// immutable-inode flag (chattr +i equivalent, via FS_IOC_SETFLAGS) for root,
// where even CAP_DAC_OVERRIDE cannot create a file inside an immutable
// directory. Empirically verified on the ci-omnipus worker's overlayfs-backed
// /tmp: `chattr +i <dir> && touch <dir>/x` as root fails EPERM.
//
// Traces to: ADR-052 §6.4 Item 5 fix-wave-2 finding #2.
package gateway

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// fsImmutableFl mirrors linux/fs.h's FS_IMMUTABLE_FL (0x00000010). Not
// exported by golang.org/x/sys/unix — that package only carries the
// FS_IOC_GETFLAGS/FS_IOC_SETFLAGS ioctl REQUEST numbers (arch-specific,
// already correctly generated per GOARCH in its vendored
// zerrors_linux_*.go files); the inode-flag VALUES the request reads/writes
// are a separate, stable kernel UAPI constant this file defines directly.
const fsImmutableFl = 0x00000010

// blockNewFilesInDir makes dir immune to new-file creation for the life of
// the calling test, correctly under both an unprivileged user (local dev)
// and root (ci-omnipus). The atomic-write path this defends against
// (fileutil.WriteFileAtomic) always calls os.CreateTemp(dir, ".tmp-*")
// before its rename — blocking file creation in dir is therefore sufficient
// to fail any write targeting a file inside it, regardless of which
// specific file.
//
//   - non-root: chmod 0o500 on dir strips write, so os.CreateTemp can no
//     longer create the dentry.
//   - root: sets the immutable inode flag via FS_IOC_SETFLAGS. If the
//     underlying filesystem doesn't implement the ioctl at all
//     (ENOTSUP/EOPNOTSUPP — some overlay/network filesystems), the test
//     MUST t.Skip with an explicit reason rather than silently falling
//     through to an unblocked directory, which would reproduce exactly the
//     false-pass failure mode this helper exists to avoid.
//
// The returned restore func clears whichever block was applied. Callers
// MUST invoke it directly (not only via t.Cleanup) BEFORE their
// t.TempDir()'s auto-cleanup runs — an immutable directory cannot be
// os.RemoveAll'd, and t.TempDir()'s cleanup runs via a deferred/registered
// t.Cleanup itself, so ordering is NOT guaranteed against a caller's own
// later-registered t.Cleanup(restore) alone; call restore inline at the
// point the block is no longer needed, and additionally register it via
// t.Cleanup as a backstop against an early return/panic.
func blockNewFilesInDir(t *testing.T, dir string) (restore func()) {
	t.Helper()

	if os.Getuid() != 0 {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatalf("chmod 0o500 %s: %v", dir, err)
		}
		return func() { _ = os.Chmod(dir, 0o700) }
	}

	f, err := os.Open(dir)
	if err != nil {
		t.Fatalf("open %s for immutable-flag ioctl: %v", dir, err)
	}
	defer f.Close()

	origFlags, gerr := unix.IoctlGetUint32(int(f.Fd()), unix.FS_IOC_GETFLAGS)
	if gerr != nil {
		if isIoctlUnsupported(gerr) {
			t.Skipf("FS_IOC_GETFLAGS unsupported on this filesystem (running as root, immutable-flag path required): %v", gerr)
		}
		t.Fatalf("FS_IOC_GETFLAGS %s: %v", dir, gerr)
	}

	newFlags := int(origFlags) | fsImmutableFl
	if serr := unix.IoctlSetPointerInt(int(f.Fd()), unix.FS_IOC_SETFLAGS, newFlags); serr != nil {
		if isIoctlUnsupported(serr) {
			t.Skipf("FS_IOC_SETFLAGS (immutable) unsupported on this filesystem (running as root, immutable-flag path required): %v", serr)
		}
		t.Fatalf("FS_IOC_SETFLAGS(immutable) %s: %v", dir, serr)
	}

	cleared := false
	return func() {
		if cleared {
			return
		}
		cleared = true
		cf, oerr := os.Open(dir)
		if oerr != nil {
			// Best-effort: nothing more we can safely do without failing a
			// possibly-already-completed test from inside a cleanup func.
			return
		}
		defer cf.Close()
		_ = unix.IoctlSetPointerInt(int(cf.Fd()), unix.FS_IOC_SETFLAGS, int(origFlags))
	}
}

// isIoctlUnsupported reports whether err is the specific "this filesystem
// does not implement this ioctl" shape (ENOTSUP/EOPNOTSUPP — the same errno
// value on Linux, checked as both since the two are distinct named
// constants), as opposed to a genuine failure (e.g. an unrelated EPERM)
// that should fail the test loudly instead of skipping it.
func isIoctlUnsupported(err error) bool {
	return errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP)
}
