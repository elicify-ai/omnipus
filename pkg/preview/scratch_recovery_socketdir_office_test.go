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

// SEC-OFFICE-01 (security review 2026-11-06) and T-97 (§3.4 D2, D4): crash
// recovery deletes the path in the owner record's socket_dir. The converter
// can rewrite a record inside its own scratch, so the fixtures here prove the
// deletion stays bounded by what the gateway itself recorded:
//
//	traversal_to_outside_sentinel  a legit-prefixed path that climbs out
//	                               through parent directories must not
//	                               delete anything outside (spec §3.1–3.3
//	                               closure property: the sentinel survives)
//	symlinked_socket_folder        a recorded link is removed itself; its
//	                               outside target and contents survive
//	                            T-97 (c): the fixture uses randomID(4) (8 hex)
//	                            so the name matches what makeSocketDir creates
//	                            and the removal step is actually reached; the
//	                            link's absence is asserted with Lstat before
//	                            cleanup could remove it
//
// Fixtures are crash-recovery records only; nothing is killed (every
// gateway/launcher identity reads dead). The traversal fixture needs ONE
// real, uniquely named folder under the legitimate /private/tmp prefix,
// because a kernel path traversal cannot walk through a component that does
// not exist; that folder is /private/tmp/o-<8 random hex>, created 0700 and
// removed by cleanup — it is the only fixture path outside the test's own
// temp folders, and this file is darwin-only because unprivileged creation of
// /private/tmp is impossible on the Linux CI leg (no other platform has a
// socket folder, §3.4 D7).
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
		// climb away, which is exactly what a prefix check fails to do.
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

	// T-97 (c). randomID(4) — 8 hex, the exact name shape makeSocketDir
	// creates — so the recorded link reaches the removal step instead of
	// being refused by the damaged-record guard before it.
	t.Run("symlinked_socket_folder_leaves_target_intact", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(t.TempDir(), "outside")
		require.NoError(t, os.MkdirAll(target, 0o700))
		keep := filepath.Join(target, "keep.txt")
		require.NoError(t, os.WriteFile(keep, []byte("must survive"), 0o600))

		linkID, err := randomID(4)
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

		// Lstat before the t.Cleanup could remove the link: its absence here
		// proves the recorded-socket-folder removal line really ran.
		_, lstatErr := os.Lstat(link)
		require.ErrorIs(t, lstatErr, os.ErrNotExist,
			"T-97 (c): the recorded link itself must be removed by recovery")
		require.DirExists(t, target, "the link's outside target must survive recovery")
		require.FileExists(t, keep, "the target's contents must survive recovery")
		_, err = os.Stat(dir)
		require.ErrorIs(t, err, os.ErrNotExist, "the tampered scratch folder itself is still reaped")
		require.Equal(t, 1, report.Removed)
		require.Zero(t, report.Killed, "no live child is in this fixture")
	})
}

// T-97 (a), §3.4 D2: the socket folder is recorded when it is created, not at
// step 3 — a gateway that crashes after makeSocketDir but before the running
// publish still leaves a record recovery can act on, so the empty socket
// folder does not survive the crash. On the pre-amendment order (publish only
// at running) this folder is orphaned forever.
func TestPreviewRecovery_SocketFolderRecordedAtCreation(t *testing.T) {
	start, err := processStart(os.Getpid())
	require.NoError(t, err)
	home := t.TempDir()

	// The real call machinery, with the gateway identity reading dead: this
	// is exactly the state after a crash between makeSocketDir and running.
	c, err := newCall(home, os.Getpid(), "not-current-start")
	require.NoError(t, err)
	socketDir, err := c.makeSocketDir()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(socketDir, "/private/tmp/o-"), "instrument: the genuine socket folder uses the legitimate prefix")
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	published := readPreviewOwner(t, c)
	require.Equal(t, socketDir, published.SocketDir,
		"T-97 (a) §3.4 D2: creating the socket folder publishes it at once")

	var report RecoveryReport
	recoverOne(filepath.Dir(c.dir), os.Getpid(), start, &report)

	_, err = os.Stat(socketDir)
	require.ErrorIs(t, err, os.ErrNotExist,
		"T-97 (a): a socket folder the gateway recorded at creation must not survive a crash before running")
	_, err = os.Stat(c.dir)
	require.ErrorIs(t, err, os.ErrNotExist, "the crashed call's folder is reaped")
	require.Equal(t, 1, report.Removed)
	require.Zero(t, report.Killed, "nothing had started in this fixture")
}

// T-97 (b), §3.4 D3/D4: recovery removes only the socket folder the trusted
// record names — a folder with a valid name that no record names survives.
// Recovery never sweeps /private/tmp.
func TestPreviewRecovery_UnnamedSocketFolderSurvives(t *testing.T) {
	start, err := processStart(os.Getpid())
	require.NoError(t, err)
	home := t.TempDir()

	unnamedID, err := randomID(4)
	require.NoError(t, err)
	unnamed := "/private/tmp/o-" + unnamedID
	require.NoError(t, os.Mkdir(unnamed, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(unnamed, "someone-else"), []byte("not ours"), 0o600))
	t.Cleanup(func() { _ = os.RemoveAll(unnamed) })

	c, err := newCall(home, os.Getpid(), "not-current-start")
	require.NoError(t, err)
	recorded, err := c.makeSocketDir()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(recorded) })
	require.NoError(t, c.running(procIdentity{PID: 999_999, Start: "dead-launcher", Executable: "/trusted/omnipus"}, nil))
	require.NotEqual(t, recorded, unnamed, "instrument: the two fixtures are different folders")
	published := readPreviewOwner(t, c)
	require.Equal(t, recorded, published.SocketDir,
		"T-97 (b) §3.4 D1: the trusted record in the call folder names the socket folder recovery may remove")

	var report RecoveryReport
	recoverOne(filepath.Dir(c.dir), os.Getpid(), start, &report)

	_, err = os.Stat(unnamed)
	require.NoError(t, err,
		"T-97 (b): a valid-name socket folder no record names must survive recovery")
	data, err := os.ReadFile(filepath.Join(unnamed, "someone-else"))
	require.NoError(t, err)
	require.Equal(t, "not ours", string(data), "the unnamed folder's contents survive")
	_, err = os.Stat(recorded)
	require.ErrorIs(t, err, os.ErrNotExist, "the recorded socket folder is still removed")
	_, err = os.Stat(c.dir)
	require.ErrorIs(t, err, os.ErrNotExist, "the crashed call's folder is reaped")
	require.Equal(t, 1, report.Removed)
	require.Zero(t, report.Killed)
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
	require.NoError(t, c.running(procIdentity{PID: 999_999, Start: "dead-launcher", Executable: "/trusted/omnipus"}, nil))
	require.NoError(t, c.idle())
	published := readPreviewOwner(t, c)
	require.Equal(t, socketDir, published.SocketDir, "instrument: the genuine record really names its socket folder")

	var report RecoveryReport
	recoverOne(filepath.Dir(c.dir), os.Getpid(), start, &report)

	_, err = os.Stat(socketDir)
	require.ErrorIs(t, err, os.ErrNotExist, "a genuine record's socket folder must still be removed")
	_, err = os.Stat(c.dir)
	require.ErrorIs(t, err, os.ErrNotExist, "the genuine scratch folder must still be removed")
	require.Equal(t, 1, report.Removed)
	require.Zero(t, report.Killed, "no live child is in this fixture")
	require.Empty(t, report.Warnings)
}
