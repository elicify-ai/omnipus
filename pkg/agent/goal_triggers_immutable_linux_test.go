//go:build linux

// goal_triggers_immutable_linux_test.go — Linux implementation of
// blockSessionDirWrites, the shared test helper the M3 persist-failure rigs
// (TestGoalAdjudication_RoundAdvancePersistFailure_Aborts_M3 and
// TestBareClaim_RoundAdvancePersistFailure_Aborts_M3Sibling) use to force a
// real store-level SetMeta failure. It mirrors pkg/gateway's proven
// blockNewFilesInDir (rest_plan_stop_immutable_linux_test.go) verbatim in
// approach; see that file's doc comment for the full rationale and the two
// rejected prior approaches (chmod-alone and non-empty-directory swap).
//
// The ci-omnipus CI worker runs the Go suite AS ROOT, where CAP_DAC_OVERRIDE
// makes a permission-stripped (chmod 0o500) directory block nothing: store.
// SetMeta's atomic temp+rename silently succeeds and the test's "SetMeta
// unexpectedly succeeded on the read-only store" setup invariant fatals — a
// false-failure that is the exact root-defeat the planning-goals epic hit.
// chmod suffices for an unprivileged local run, but the Linux immutable-inode
// flag (chattr +i equivalent, via FS_IOC_SETFLAGS) blocks root too — even
// CAP_DAC_OVERRIDE cannot create a file inside an immutable directory.
// Empirically verified on the ci-omnipus worker's overlayfs-backed /tmp.
//
// Traces to: ADR-053 Phase-2 §1/§2 M3 gate-honesty rigs.
package agent

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// fsImmutableFl mirrors linux/fs.h's FS_IMMUTABLE_FL (0x00000010). Not
// exported by golang.org/x/sys/unix — see the gateway twin for why.
const fsImmutableFl = 0x00000010

// blockSessionDirWrites makes dir immune to new-file creation for the life of
// the calling test, correctly under both an unprivileged user (local dev) and
// root (ci-omnipus). The atomic-write path this defends against
// (fileutil.WriteFileAtomic, used by store.SetMeta) always calls
// os.CreateTemp(dir, ".tmp-*") before its rename — blocking file creation in
// dir is therefore sufficient to fail any write targeting a file inside it.
//
//   - non-root: chmod 0o500 on dir strips write, so os.CreateTemp can no
//     longer create the dentry.
//   - root: sets the immutable inode flag via FS_IOC_SETFLAGS. If the
//     underlying filesystem doesn't implement the ioctl at all
//     (ENOTSUP/EOPNOTSUPP — some overlay/network filesystems), the test MUST
//     t.Skip with an explicit reason rather than silently fall through to an
//     unblocked directory (which would reproduce the false-pass/failure mode
//     this helper exists to avoid).
//
// The returned restore func clears whichever block was applied. Callers MUST
// invoke it directly (not only via t.Cleanup) BEFORE their t.TempDir()'s
// auto-cleanup runs — an immutable directory cannot be os.RemoveAll'd, and
// t.TempDir()'s cleanup is itself a registered t.Cleanup whose ordering
// against a caller's later t.Cleanup(restore) is NOT guaranteed; call restore
// inline at the point the block is no longer needed, and additionally
// register it via t.Cleanup as a backstop against an early return/panic. The
// returned func is idempotent, so calling it inline then again from
// t.Cleanup is safe.
func blockSessionDirWrites(t *testing.T, dir string) (restore func()) {
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
		if isFsIoctlUnsupported(gerr) {
			t.Skipf("FS_IOC_GETFLAGS unsupported on this filesystem (running as root, immutable-flag path required): %v", gerr)
		}
		t.Fatalf("FS_IOC_GETFLAGS %s: %v", dir, gerr)
	}

	newFlags := int(origFlags) | fsImmutableFl
	if serr := unix.IoctlSetPointerInt(int(f.Fd()), unix.FS_IOC_SETFLAGS, newFlags); serr != nil {
		if isFsIoctlUnsupported(serr) {
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

// isFsIoctlUnsupported reports whether err is the specific "this filesystem
// does not implement this ioctl" shape (ENOTSUP/EOPNOTSUPP — the same errno
// value on Linux, checked as both since the two are distinct named
// constants), as opposed to a genuine failure (e.g. an unrelated EPERM) that
// should fail the test loudly instead of skipping it.
func isFsIoctlUnsupported(err error) bool {
	return errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP)
}
