package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// Capture only the refusal journey, not an earlier create/get warning. The
// marker has no fixture path or check ID, so it cannot satisfy the diagnostic.
func t11Q48CaptureRefusalLogs(t *testing.T) *capturedLogs {
	t.Helper()
	logs := captureSlogWarnings(t)
	const control = "t11-q48-server-capture-control"
	slog.Warn(control)
	if got := logs.matching(control); len(got) != 1 {
		t.Fatalf("Q48 SETUP: server-log capture did not observe its control: %q", got)
	}
	return logs
}

// Q48: the known fixture locations must be absent in the refusal and present
// with the failed check in a server record. A link may itself be forbidden, so
// the logged failure can identify either that link or its resolved target.
func t11Q48AssertServerOnlyPath(t *testing.T, logs *capturedLogs, refusal, check string, paths ...string) {
	t.Helper()
	var spellings []string
	seen := make(map[string]bool)
	add := func(path string) {
		if path == "" {
			t.Fatal("Q48 SETUP: an empty fixture path cannot prove privacy")
		}
		if !seen[path] {
			seen[path] = true
			spellings = append(spellings, path)
		}
	}
	for _, path := range paths {
		add(path)
		// Resolve the parent separately: the leaf may be the intentionally
		// missing program, or a link whose own location caused the refusal.
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			t.Fatalf("Q48 SETUP: resolve fixture parent: %v", err)
		}
		add(filepath.Join(parent, filepath.Base(path)))
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			add(resolved)
		} else if !os.IsNotExist(err) {
			t.Fatalf("Q48 SETUP: resolve existing fixture program: %v", err)
		}
	}
	if len(spellings) == 0 {
		t.Fatal("Q48 SETUP: no fixture locations supplied")
	}
	for _, path := range spellings {
		if l11274ReasonNamesPath(refusal, path) {
			t.Errorf("Q48: program path %q leaked into the refusal %q", path, refusal)
		}
	}
	// Each candidate line already contains the independently expected failed
	// check. Require the same record to contain a literal or Go-quoted path.
	diagnostics := logs.matching(check)
	for _, line := range diagnostics {
		for _, path := range spellings {
			if l11274ReasonNamesPath(line, path) {
				t.Logf("Q48: failed check %q and program location captured in server diagnostics", check)
				return
			}
		}
	}
	t.Errorf("Q48: no server diagnostic pairs check %q with the fixture locations %q; captured matching records: %q", check, spellings, diagnostics)
}
