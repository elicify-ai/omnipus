//go:build darwin

package preview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// SEC-OFFICE-01 (security review 2026-11-06): crash recovery deletes the path
// in the owner record's socket_dir after validating it with only a
// strings.HasPrefix check against the legitimate /private/tmp/o- prefix. The
// converter can rewrite that record (its sandbox grants read-write over the
// whole scratch tree), so a record may name a path that starts with the
// prefix but climbs out through parent-directory steps to a folder the
// converter could never write — and the next gateway startup would
// recursively delete it (spec §3.1–3.3: recovery owns only the destinations
// the gateway itself created; the closure property is that an outside
// sentinel survives).
//
// Fixtures here are crash-recovery records only; nothing is killed (every
// gateway/launcher identity reads dead). The traversal fixture needs ONE
// real, uniquely named folder under the legitimate /private/tmp prefix,
// because a kernel path traversal cannot walk through a component that does
// not exist; that folder is /private/tmp/o-<8 random hex>, created 0700 and
// removed by cleanup — it is the only fixture path outside the test's own
// temp folders, and this file is darwin-only because unprivileged creation of
// /private/tmp is impossible on the Linux CI leg. The top-level symlink
// variant is the contrast the security report draws: recovery removing a
// legit-prefixed symlink itself is fine — the target it points at must
// survive — while traversal through the prefix must not delete anything
// outside.
func TestPreviewRecovery_TamperedSocketDirCannotEscapeItsPrefix(t *testing.T) {
	start, err := processStart(os.Getpid())
	require.NoError(t, err)

	t.Run("traversal_to_outside_sentinel", func(t *testing.T) {
		home := t.TempDir()
		sentinel := filepath.Join(t.TempDir(), "sentinel")
		require.NoError(t, os.MkdirAll(sentinel, 0o700))
		keep := filepath.Join(sentinel, "keep.txt")
		require.NoError(t, os.WriteFile(keep, []byte("must survive"), 0o600))

		prefixID, err := randomID(8)
		require.NoError(t, err)
		prefixDir := "/private/tmp/o-" + prefixID
		require.NoError(t, os.Mkdir(prefixDir, 0o700))
		t.Cleanup(func() { _ = os.RemoveAll(prefixDir) })

		// The tampered record: state preparing, a gateway identity that reads
		// dead, and a socket_dir with the legitimate prefix and a climb to the
		// sentinel outside. Raw string — filepath.Clean would resolve the
		// climb away, which is exactly what the prefix check fails to do.
		socketDir := "/private/tmp/o-" + prefixID + "/../../../" + strings.TrimPrefix(sentinel, "/")
		require.True(t, strings.HasPrefix(socketDir, "/private/tmp/o-"),
			"instrument: the tampered path really carries the legitimate prefix")
		dir := filepath.Join(scratchRoot(home), "tampered")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		record, err := json.Marshal(ownerRecord{
			State: statePreparing, GatewayPID: os.Getpid(), GatewayStart: "not-current-start", SocketDir: socketDir,
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, ownerFile), record, 0o600))

		var report RecoveryReport
		recoverOne(dir, os.Getpid(), start, &report)

		require.DirExists(t, sentinel,
			"SEC-OFFICE-01: recovery deleted a destination derived from converter-rewritable metadata — the outside sentinel must survive")
		require.FileExists(t, keep, "the sentinel's contents must survive recovery")
		_, err = os.Stat(dir)
		require.ErrorIs(t, err, os.ErrNotExist, "the tampered scratch folder itself is still reaped")
		require.Equal(t, 1, report.Removed, "normal AD-8 reaping continues alongside the refusal")
		require.Zero(t, report.Killed, "no live child is in this fixture")
	})

	t.Run("symlinked_socket_folder_leaves_target_intact", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(t.TempDir(), "outside")
		require.NoError(t, os.MkdirAll(target, 0o700))
		keep := filepath.Join(target, "keep.txt")
		require.NoError(t, os.WriteFile(keep, []byte("must survive"), 0o600))

		linkID, err := randomID(8)
		require.NoError(t, err)
		link := "/private/tmp/o-" + linkID
		require.NoError(t, os.Symlink(target, link))
		t.Cleanup(func() { _ = os.RemoveAll(link) })

		dir := filepath.Join(scratchRoot(home), "linked")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		record, err := json.Marshal(ownerRecord{
			State: statePreparing, GatewayPID: os.Getpid(), GatewayStart: "not-current-start", SocketDir: link,
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, ownerFile), record, 0o600))

		var report RecoveryReport
		recoverOne(dir, os.Getpid(), start, &report)

		require.DirExists(t, target, "the symlink's outside target must survive recovery")
		require.FileExists(t, keep, "the target's contents must survive recovery")
		_, err = os.Stat(dir)
		require.ErrorIs(t, err, os.ErrNotExist, "the tampered scratch folder itself is still reaped")
		require.Equal(t, 1, report.Removed)
		require.Zero(t, report.Killed, "no live child is in this fixture")
	})
}

// Positive control: a GENUINE record — written by the real call machinery, its
// socket folder created by makeSocketDir — must still lose its socket folder
// and scratch folder at recovery. A fix that over-tightens into refusing every
// metadata-named destination fails here.
func TestPreviewRecovery_GenuineRecordSocketFolderStillRemoved(t *testing.T) {
	start, err := processStart(os.Getpid())
	require.NoError(t, err)
	home := t.TempDir()

	// The call's gateway identity reads dead (this process, a start time it
	// never had), simulating the crash. running() then idle() leaves a
	// preparing record that carries the real socket folder.
	c, err := newCall(home, os.Getpid(), "not-current-start")
	require.NoError(t, err)
	socketDir, err := c.makeSocketDir()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(socketDir, "/private/tmp/o-"), "instrument: the genuine socket folder uses the legitimate prefix")
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	require.NoError(t, c.running(procIdentity{PID: 999_999, Start: "dead-launcher", Executable: "/trusted/omnipus"}, 999_999, nil))
	require.NoError(t, c.idle())
	published := readPreviewOwner(t, c)
	require.Equal(t, socketDir, published.SocketDir, "instrument: the genuine record really names its socket folder")

	var report RecoveryReport
	recoverOne(c.dir, os.Getpid(), start, &report)

	_, err = os.Stat(socketDir)
	require.ErrorIs(t, err, os.ErrNotExist, "a genuine record's socket folder must still be removed")
	_, err = os.Stat(c.dir)
	require.ErrorIs(t, err, os.ErrNotExist, "the genuine scratch folder must still be removed")
	require.Equal(t, 1, report.Removed)
	require.Zero(t, report.Killed, "no live child is in this fixture")
	require.Empty(t, report.Warnings)
}
