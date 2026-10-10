package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// U7 — gateway cancel-signature shim deletion (DEL-17; spec
// docs/internal/specs/session-core-spec.md §"U7" and DEL-17 row).
//
// DEL-17: "pkg/gateway/websocket_cancel.go::u11CollectDescendantSessionIDs
// signature shim; stale prose about the earlier u11 name in
// pkg/agent/cancel.go" — DELETE. "CollectDescendantSessionIDs and one Stop
// path, preserving returned errors/selected execution. Nothing replaces the
// gateway wrapper kept solely for an older signature."
//
// The gateway wrapper exists only to keep an older name/signature alive for
// callers (pkg/gateway/rest_sessions.go, the U11 unit tests). DEL-17 removes
// it and re-points the callers at agent.CollectDescendantSessionIDs. The stale
// u11 name must therefore vanish from non-test sources in BOTH packages.

// sessionCoreU7AbsentAcrossDirs proves no non-test .go file under any of `dirs`
// mentions `symbol`. Carries the two controls a real absence instrument needs
// (known-present + injected-forbidden), modeled on the U15 instrument.
//
// `presentControlFile`/`presentControl` must live under one of `dirs` so the
// present-control is proven against the same sweep.
func sessionCoreU7AbsentAcrossDirs(t *testing.T, symbol string, dirs []string, presentControlFile, presentControl string) {
	t.Helper()

	ctrl, err := os.ReadFile(presentControlFile)
	if err != nil {
		t.Fatalf("U7 instrument: present-control read %s: %v", presentControlFile, err)
	}
	if !strings.Contains(string(ctrl), presentControl) {
		t.Fatalf("U7 instrument broken: present-control %q not found in %s", presentControl, presentControlFile)
	}

	injected := filepath.Join(t.TempDir(), "injected_fixture.txt")
	if err := os.WriteFile(injected, []byte(symbol), 0o600); err != nil {
		t.Fatalf("U7 instrument: write injected fixture: %v", err)
	}
	injectedData, err := os.ReadFile(injected)
	if err != nil {
		t.Fatalf("U7 instrument: read injected fixture: %v", err)
	}
	if !strings.Contains(string(injectedData), symbol) {
		t.Fatalf("U7 instrument broken: injected forbidden token %q not detected", symbol)
	}

	sawControlFile := false
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("U7 instrument: glob %s: %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			data, err := os.ReadFile(f)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				t.Fatalf("U7 sweep: read %s: %v", f, err)
			}
			if strings.Contains(string(data), symbol) {
				t.Errorf("%s still references %q — DEL-17 requires the u11 signature-compat shim (and the stale prose naming it) removed with a clean source sweep", f, symbol)
			}
			if filepath.Clean(f) == filepath.Clean(presentControlFile) {
				sawControlFile = true
			}
		}
	}
	if !sawControlFile {
		t.Fatalf("U7 instrument broken: present-control file %s not among the swept sources", presentControlFile)
	}
}

// TestSessionCoreU7_U11CancelShimRemoved is the DEL-17 K half. RED on the
// pre-cut code: pkg/gateway/websocket_cancel.go still declares
// `func u11CollectDescendantSessionIDs(...)` and pkg/agent/cancel.go's prose
// still names the earlier u11 symbol.
func TestSessionCoreU7_U11CancelShimRemoved(t *testing.T) {
	sessionCoreU7AbsentAcrossDirs(t,
		"u11CollectDescendantSessionIDs",
		[]string{".", "../agent"},
		"websocket_cancel.go", "CollectDescendantSessionIDs",
	)
}
