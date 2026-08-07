package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-057 U20 (W18a) — RemoveSessionUploadsTree primitive tests.
//
// Per the spec's governing constraint ("almost every failure in this
// migration is success-shaped") and binding rule 4, every assertion here that
// something is GONE is preceded by a real, on-disk, non-empty positive lower
// bound proving the thing existed in the first place — otherwise a broken
// primitive (or a broken test) would pass vacuously against an empty
// directory. Nothing here uses a mock or fake filesystem: every tree is a
// real directory under a real t.TempDir(), verified with os.Stat/os.ReadDir
// before and after.

// mustSeedUploadsDir creates <OMNIPUS_HOME>/uploads/<id> with a real,
// non-empty marker file inside and returns its path. It fails the test
// immediately if SessionUploadsDir rejects id or the directory cannot be
// created — a test that cannot seed real state must not silently continue.
func mustSeedUploadsDir(t *testing.T, id string) string {
	t.Helper()
	dir, ok := SessionUploadsDir(id)
	if !ok {
		t.Fatalf("SessionUploadsDir(%q) rejected a supposedly-safe id", id)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("seed uploads dir %s: %v", dir, err)
	}
	marker := filepath.Join(dir, "upload.bin")
	if err := os.WriteFile(marker, []byte("payload"), 0o600); err != nil {
		t.Fatalf("seed uploads marker in %s: %v", dir, err)
	}
	return dir
}

// assertDirExistsNonEmpty is the positive lower bound every removal
// assertion in this file is required to establish first (binding rule 4).
func assertDirExistsNonEmpty(t *testing.T, dir string) {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("precondition failed: %s does not exist as a directory (err=%v)", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("precondition failed: could not read %s: %v", dir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("precondition failed: %s exists but has no seeded content", dir)
	}
}

func assertDirGone(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat returned err=%v", dir, err)
	}
}

// TestRemoveSessionUploadsTree_RemovesListedKeepsUnrelated is the direct
// regression for BDD-78 / FR-071 (though the full cascade wiring through a
// real session delete is U18's TestUploadsCascadeDeleteAcrossDescendants —
// this test proves the primitive itself, in isolation, does the file-system
// work correctly for a multi-depth descendant set): a parent plus
// descendants at depth 1 and depth 2 are all removed, and an unrelated
// sibling session's uploads survive untouched.
func TestRemoveSessionUploadsTree_RemovesListedKeepsUnrelated(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	const (
		parentID     = "parent-adr057-sess"
		childID      = "child-depth1-adr057-sess"
		grandchildID = "grandchild-depth2-adr057-sess"
		siblingID    = "unrelated-sibling-adr057-sess"
	)

	parentDir := mustSeedUploadsDir(t, parentID)
	childDir := mustSeedUploadsDir(t, childID)
	grandchildDir := mustSeedUploadsDir(t, grandchildID)
	siblingDir := mustSeedUploadsDir(t, siblingID)

	// Positive lower bound: all four real trees exist and are non-empty
	// before RemoveSessionUploadsTree is ever called.
	for _, dir := range []string{parentDir, childDir, grandchildDir, siblingDir} {
		assertDirExistsNonEmpty(t, dir)
	}

	if err := RemoveSessionUploadsTree([]string{parentID, childID, grandchildID}); err != nil {
		t.Fatalf("RemoveSessionUploadsTree returned an error for a clean removal: %v", err)
	}

	for _, dir := range []string{parentDir, childDir, grandchildDir} {
		assertDirGone(t, dir)
	}

	// The sibling was never in the ids list and MUST survive, byte for byte.
	assertDirExistsNonEmpty(t, siblingDir)
	entries, err := os.ReadDir(siblingDir)
	if err != nil {
		t.Fatalf("unrelated sibling dir %s unreadable after cascade: %v", siblingDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("unrelated sibling dir %s: want 1 entry, got %d", siblingDir, len(entries))
	}
	content, err := os.ReadFile(filepath.Join(siblingDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("unrelated sibling dir %s: could not read surviving file: %v", siblingDir, err)
	}
	if string(content) != "payload" {
		t.Fatalf("unrelated sibling dir %s: content changed: got %q", siblingDir, content)
	}
}

// TestRemoveSessionUploadsTree_SkipsUnsafeAndEmptyIDsWithoutError asserts the
// batch behaviour BDD-79 requires: a path-unsafe or empty id in the list is
// skipped (mirroring SessionUploadsDir's existing ("", false) contract), not
// treated as a fatal error that aborts the whole cascade — while the one
// genuinely valid id in the same call is still removed.
func TestRemoveSessionUploadsTree_SkipsUnsafeAndEmptyIDsWithoutError(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	const validID = "valid-adr057-sess"
	validDir := mustSeedUploadsDir(t, validID)
	assertDirExistsNonEmpty(t, validDir)

	unsafeIDs := []string{"", "..", "../escape", "a/b", `a\b`, ".hidden"}
	// Positive lower bound: confirm every one of these is actually rejected
	// by SessionUploadsDir before leaning on that rejection inside the
	// primitive — otherwise this test would pass even if the reject logic
	// were broken, by accident, for the wrong reason.
	rejected := 0
	for _, id := range unsafeIDs {
		if _, ok := SessionUploadsDir(id); !ok {
			rejected++
		}
	}
	if rejected != len(unsafeIDs) {
		t.Fatalf("expected all %d unsafe ids to be rejected by SessionUploadsDir, got %d", len(unsafeIDs), rejected)
	}

	ids := append([]string{validID}, unsafeIDs...)
	if err := RemoveSessionUploadsTree(ids); err != nil {
		t.Fatalf("RemoveSessionUploadsTree returned an error for a batch containing skippable unsafe ids: %v", err)
	}
	assertDirGone(t, validDir)
}

// TestRemoveSessionUploadsTree_MissingDirectoryIsNotAnError covers a
// descendant that never uploaded any media: SessionUploadsDir resolves a
// real path for it, but nothing was ever created there. This must not be
// reported as a failure.
func TestRemoveSessionUploadsTree_MissingDirectoryIsNotAnError(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	const neverUploadedID = "never-uploaded-adr057-sess"
	dir, ok := SessionUploadsDir(neverUploadedID)
	if !ok {
		t.Fatalf("SessionUploadsDir rejected a safe id unexpectedly")
	}
	// Positive lower bound for THIS test's shape: prove the path really is
	// absent (not merely never checked) before asserting removal is a no-op.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("precondition failed: %s unexpectedly exists (err=%v)", dir, err)
	}

	if err := RemoveSessionUploadsTree([]string{neverUploadedID}); err != nil {
		t.Fatalf("RemoveSessionUploadsTree errored on a descendant that never created a dir: %v", err)
	}
}

// TestRemoveSessionUploadsTree_ReturnsRealErrorInsteadOfSwallowing is the
// direct test for this spec's governing constraint: a removal that
// genuinely fails at the OS level must be surfaced to the caller, not eaten
// silently the way AppendTranscript's meta-read failure is (unified.go
// :819-823). It forces a REAL permission-denied error from os.RemoveAll (no
// mock, no fake FS) by stripping write permission on one session's own
// uploads directory, and proves the batch still attempts — and completes —
// every other id.
func TestRemoveSessionUploadsTree_ReturnsRealErrorInsteadOfSwallowing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the DAC permission check this test relies on")
	}
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	const (
		failingID = "unremovable-adr057-sess"
		otherID   = "removable-sibling-adr057-sess"
	)

	failingDir := mustSeedUploadsDir(t, failingID)
	otherDir := mustSeedUploadsDir(t, otherID)

	// Positive lower bound: both real trees exist before sabotage.
	assertDirExistsNonEmpty(t, failingDir)
	assertDirExistsNonEmpty(t, otherDir)

	// Strip write permission on the FAILING session's own directory (not its
	// shared uploads-root parent, which stays writable so the sibling's
	// removal is unaffected). Without write on failingDir, unlinking the
	// marker file inside it fails with a real "permission denied" from the
	// kernel — verified empirically: os.RemoveAll on a dir chmod'd 0o555
	// returns a non-nil unlinkat error and leaves the directory in place.
	if err := os.Chmod(failingDir, 0o555); err != nil {
		t.Fatalf("chmod failing dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(failingDir, 0o755) })

	err := RemoveSessionUploadsTree([]string{failingID, otherID})
	if err == nil {
		t.Fatalf("expected a non-nil error surfacing the real removal failure for %q, got nil", failingID)
	}

	// The error must name the session that actually failed, not be a bare
	// generic message that could describe any failure.
	if !strings.Contains(err.Error(), failingID) {
		t.Fatalf("error %q does not identify the failing session id %q", err.Error(), failingID)
	}

	// The failing directory's content survives (removal genuinely failed —
	// this is an observable artefact, not just an error return).
	assertDirExistsNonEmpty(t, failingDir)

	// The batch did not abort on the first failure: the OTHER id, sharing the
	// same uploads root, was still removed.
	assertDirGone(t, otherDir)
}
