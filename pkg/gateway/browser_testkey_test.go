package gateway

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// browserTestKey mints a browser.BrowsingKey for this package's tests.
//
// A BrowsingKey has no exported literal constructor by design (ADR-075 D1.11):
// the only way to get one is to resolve it, so a caller cannot mint a shared
// browser by accident. Tests are no exception — this resolves a real key
// against a throwaway workspace file rather than fabricating a string.
func browserTestKey(t *testing.T, workspaceID string) browser.BrowsingKey {
	t.Helper()
	home := browserTestKeyHome(t)
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("browserTestKey: mkdir workspaces: %v", err)
	}
	path := filepath.Join(dir, workspaceID+".json")
	body := []byte(`{"id":"` + workspaceID + `","core_team":["browser-test-probe"]}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("browserTestKey: write workspace: %v", err)
	}
	k, err := browser.ResolveBrowsingKeyForAgent(home, "browser-test-probe", workspaceID)
	if err != nil {
		t.Fatalf("browserTestKey(%q): %v", workspaceID, err)
	}
	return k
}

var (
	browserTestKeyHomeOnce sync.Once
	browserTestKeyHomeDir  string
)

// browserTestKeyHome is one process-wide scratch home for the probe workspace
// files browserTestKey writes. It is deliberately NOT t.TempDir(): two calls in
// one test must be able to mint two DIFFERENT keys, and a per-call temp dir
// would give the probe agent exactly one workspace each time, which is fine —
// but a shared dir also lets a caller mint two keys and have both stay
// resolvable, which some callers rely on.
func browserTestKeyHome(t *testing.T) string {
	t.Helper()
	browserTestKeyHomeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "omnipus-browser-testkeys-")
		if err != nil {
			t.Fatalf("browserTestKeyHome: %v", err)
		}
		browserTestKeyHomeDir = dir
	})
	return browserTestKeyHomeDir
}
