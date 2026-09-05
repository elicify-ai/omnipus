// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// R5 finding 3 — deleting a workspace must actually delete its browser
// profile, including when the browser is mid-shutdown.
//
// ADR-072 FR-043a / SC-017 make workspace deletion the ONE trigger that removes
// a profile directory, precisely because that directory holds the workspace's
// live logins. The single-shot Close-then-DeleteProfile the handler used could
// report success while leaving them on disk, and the whole point of these tests
// is that "DeleteProfile returned nil" and "the directory is gone" are
// different claims.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// newProfilePoolForTest builds a real BrowserPool over a temp $OMNIPUS_HOME and
// returns it with the profile directory for key, already populated with a file
// that stands in for the cookie jar. No Chrome is launched: every path under
// test runs with the pool's instances map empty, which is exactly the state the
// delete handler reaches after Close.
func newProfilePoolForTest(t *testing.T, workspaceID string) (*browser.BrowserPool, browser.BrowsingKey, string) {
	t.Helper()

	home := t.TempDir()
	cfg := browser.BrowserConfig{ProfileDir: filepath.Join(home, "browser", "profiles", "default")}
	pool := browser.NewBrowserPool(home, cfg)

	key, err := browser.ParseBrowsingKeyString("ws:" + workspaceID)
	if err != nil {
		t.Fatalf("ParseBrowsingKeyString: %v", err)
	}
	dir, err := pool.ProfileDirFor(key)
	if err != nil {
		t.Fatalf("ProfileDirFor: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "Default"), 0o700); err != nil {
		t.Fatalf("stage profile dir: %v", err)
	}
	// The thing the whole feature exists to protect, and therefore the thing
	// whose survival is the defect.
	if err := os.WriteFile(filepath.Join(dir, "Default", "Cookies"), []byte("session-cookie"), 0o600); err != nil {
		t.Fatalf("stage cookie jar: %v", err)
	}
	return pool, key, dir
}

// TestDeleteWorkspaceBrowserProfile_RemovesTheProfileAndItsLogins is the happy
// path, and it deliberately runs against the REAL filesystem check — no seam
// substituted — so the production confirm step is exercised, not just the one
// the next test stages.
func TestDeleteWorkspaceBrowserProfile_RemovesTheProfileAndItsLogins(t *testing.T) {
	pool, key, dir := newProfilePoolForTest(t, "ws-alpha")

	if err := deleteWorkspaceBrowserProfile(pool, key); err != nil {
		t.Fatalf("deleting a departed workspace's profile must succeed; got %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the profile directory %s must be gone after the workspace is deleted; Stat err = %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Default", "Cookies")); !os.IsNotExist(err) {
		t.Error("the deleted workspace's session cookies are still on disk — a departed client's data must depart")
	}
}

// TestDeleteWorkspaceBrowserProfile_DirectoryComesBack_IsRetriedThenReported is
// the regression for the defect.
//
// A Chrome that another goroutine was already shutting down when the delete
// arrived is still writing its cookie jar on the way down: DeleteProfile's
// RemoveAll succeeds and returns nil, and the dying browser then recreates the
// directory. The single-shot caller treated nil as "the profile is gone" and
// logged nothing at all.
//
// The oracle is the requirement, not the implementation: after this function
// returns, either the directory is gone or the caller was TOLD it is not. A
// profile that keeps coming back must never produce a silent nil.
func TestDeleteWorkspaceBrowserProfile_DirectoryComesBack_IsRetriedThenReported(t *testing.T) {
	pool, key, dir := newProfilePoolForTest(t, "ws-beta")

	// Stage the unstageable: the directory is there every time we look, which
	// is what a browser flushing state on exit produces. Counted so the retry
	// is proven to happen rather than assumed.
	looks := 0
	restore := browserProfileExistsFn
	browserProfileExistsFn = func(string) bool {
		looks++
		return true
	}
	t.Cleanup(func() { browserProfileExistsFn = restore })

	restoreSettle := browserProfileDeleteSettle
	browserProfileDeleteSettle = time.Millisecond
	t.Cleanup(func() { browserProfileDeleteSettle = restoreSettle })

	err := deleteWorkspaceBrowserProfile(pool, key)
	if err == nil {
		t.Fatal("a profile directory that is still there must NOT be reported as deleted — " +
			"this is the defect: the workspace is gone and its logins are not, and nobody is told")
	}
	if looks != browserProfileDeleteAttempts {
		t.Errorf("the delete was confirmed %d time(s), want %d — a retry is what covers a browser that "+
			"was mid-shutdown when the delete arrived", looks, browserProfileDeleteAttempts)
	}
	// The operator has to be able to act on this, which means knowing WHICH
	// directory still holds the data.
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("the error must name the directory that still holds the logins; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "logins") {
		t.Errorf("the error must say what is at stake, not just that a delete failed; got %q", err.Error())
	}
}

// TestDeleteWorkspaceBrowserProfile_ConfirmsRatherThanTrustingTheReturnValue is
// the narrow statement of the fix: DeleteProfile answering nil is a claim about
// one RemoveAll call, not about whether the directory is gone. If the two ever
// disagree, the filesystem wins.
func TestDeleteWorkspaceBrowserProfile_ConfirmsRatherThanTrustingTheReturnValue(t *testing.T) {
	pool, key, _ := newProfilePoolForTest(t, "ws-gamma")

	restoreSettle := browserProfileDeleteSettle
	browserProfileDeleteSettle = time.Millisecond
	t.Cleanup(func() { browserProfileDeleteSettle = restoreSettle })

	// Gone on the second look: the first delete raced a shutdown and lost, the
	// retry won. The caller must see success, and must have retried to get it.
	looks := 0
	restore := browserProfileExistsFn
	browserProfileExistsFn = func(string) bool {
		looks++
		return looks < 2
	}
	t.Cleanup(func() { browserProfileExistsFn = restore })

	if err := deleteWorkspaceBrowserProfile(pool, key); err != nil {
		t.Fatalf("a delete that succeeds on the retry is a success; got %v", err)
	}
	if looks != 2 {
		t.Errorf("confirmed %d time(s), want 2 — the retry must stop as soon as the directory is gone", looks)
	}
}
