package session

// ids_adr057_test.go — tests for ADR-057 W20 (pkg/session/ids.go).
//
// Per the ADR-057 session-unification spec's binding rule 5, every unit's
// new tests go in a NEW file named `<subject>_adr057_test.go`; this file is
// U1's, covering only pkg/session/ids.go (test #3, BDD-05, US-1 AS-5).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSessionIDTypes_DoNotInterconvert is test #3 from the ADR-057 spec
// ("Negative-gate positive lower bounds" table and TDD plan row 3),
// tracing to BDD-05 / US-1 Acceptance Scenario 5.
//
// This is a negative/compile-fail gate, so binding rule 4 requires it to
// prove its search is live before trusting the negative result: the
// positive lower bound stated in the spec for this exact gate is "the
// fixture file MUST exist and MUST be located by path" — asserted first,
// below, as a hard failure (never a silent skip) if the fixture is missing.
//
// BDD-05:
//
//	Given the compile-fail fixture file is present and locatable by path
//	     (asserted first; absence is a failure, not a pass)
//	When  `go build` runs over the fixture, in which a RoutingSessionID is
//	     assigned to a SessionID without conversion
//	Then  the build fails with a type error naming both type names
//	But   a build that succeeds, or a fixture that could not be found,
//	     fails the test
func TestSessionIDTypes_DoNotInterconvert(t *testing.T) {
	// --- Positive lower bound (binding rule 4): the fixture MUST exist and
	// MUST be located by path before its compile failure is ever asserted.
	// A missing/unreadable fixture is a test failure, never a pass.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("TestSessionIDTypes_DoNotInterconvert: getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "ids_no_interconvert")
	fixtureFile := filepath.Join(fixtureDir, "main.go")
	if info, statErr := os.Stat(fixtureFile); statErr != nil {
		t.Fatalf("TestSessionIDTypes_DoNotInterconvert: compile-fail fixture not located at %s: %v (binding rule 4: a missing fixture is a test failure, never a pass)", fixtureFile, statErr)
	} else if info.IsDir() {
		t.Fatalf("TestSessionIDTypes_DoNotInterconvert: expected a file at %s, found a directory", fixtureFile)
	}

	// --- Now, and only now, attempt the build and assert it fails.
	outPath := filepath.Join(t.TempDir(), "ids_no_interconvert_bin")
	cmd := exec.Command("go", "build", "-o", outPath, fixtureDir)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, buildErr := cmd.CombinedOutput()

	if buildErr == nil {
		t.Fatalf("TestSessionIDTypes_DoNotInterconvert: `go build` on the fixture unexpectedly SUCCEEDED (output: %s) — SessionID and RoutingSessionID interconverted implicitly, which FR-004 forbids", out)
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Fatalf("TestSessionIDTypes_DoNotInterconvert: build reported failure but produced a binary at %s", outPath)
	}

	output := string(out)
	if !strings.Contains(output, "SessionID") {
		t.Errorf("TestSessionIDTypes_DoNotInterconvert: build error does not name SessionID; got:\n%s", output)
	}
	if !strings.Contains(output, "RoutingSessionID") {
		t.Errorf("TestSessionIDTypes_DoNotInterconvert: build error does not name RoutingSessionID; got:\n%s", output)
	}
}
