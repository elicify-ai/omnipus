//go:build darwin || linux

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SEC-OFFICE-02 (security review 2026-11-06, sec02-root-cause.md): the search
// key removal reaches the §3.2 program-folder refusal THROUGH
// AgentLoop.MutateConfig, which holds the loop's write lock — and the refusal
// then re-entered the same non-reentrant RWMutex via GetConfig
// (MutateConfig -> writeConfigJSONLocked -> refuseNewProgramAccess ->
// GetConfig). The removal deadlocked; the pre-existing removal test times out
// on the defective build and is the primary RED. This test is the chief's
// required second one: the §3.2 check must still RUN on this path and refuse
// a newly added overlapping write entry, within the removal's own deadline —
// a fix that drops the check for this path (or a "fix" that merely stops
// hanging) must fail here. The refusal must carry the exact spec sentence,
// and a refused removal must write no config byte and leave the saved
// credential untouched.
//
// The overlap is seeded on disk AFTER the loop booted, so the published
// baseline does not contain it: exactly the "file holds a newly added write
// entry" state T-87 refuses at every other writer.
//
// The removal runs in a child process (the standard pattern for code under
// test that may never return): the fixture's registered AgentLoop.Close
// cleanup cannot complete while the deadlocked goroutine holds the write
// lock, so an in-process deadline would fail the test yet stall the whole
// package on cleanup. The child gives the removal a 10 s deadline of its own;
// on expiry it prints the SEC02-DEADLOCK marker and exits at once, skipping
// every cleanup — the hang fails in seconds instead of stalling the package.
const sec02ChildEnv = "OMNIPUS_SEC02_DEADLOCK_CHILD"

func TestRemoveSearchKey_ProgramOverlapRefusalRunsInsideConfigMutation(t *testing.T) {
	if os.Getenv(sec02ChildEnv) != "1" {
		sec02RunChild(t)
		return
	}
	sec02Child(t)
}

// sec02RunChild re-executes this test binary for the real run and turns the
// child's outcome into this test's verdict.
func sec02RunChild(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run",
		"^TestRemoveSearchKey_ProgramOverlapRefusalRunsInsideConfigMutation$", "-test.timeout", "30s", "-test.v")
	cmd.Env = append(os.Environ(), sec02ChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("SEC-OFFICE-02: the child run itself failed: %v\n%s", err, out)
	}
	switch {
	case err != nil && exitErr.ExitCode() == 3:
		t.Fatalf("SEC-OFFICE-02 RED: search-key removal deadlocked in the child — the §3.2 check re-entered the config write lock instead of refusing (marker + parked goroutine below):\n%s",
			sec02Evidence(string(out)))
	case err != nil:
		t.Fatalf("SEC-OFFICE-02: the child run failed unexpectedly (exit %d):\n%s", exitErr.ExitCode(), out)
	}
	// GREEN: the child's own assertions (exact §3.2 sentence, no config byte
	// written, credential untouched) passed inside the child; the parent
	// re-checks the refusal sentence from the child's evidence line.
	require.Contains(t, string(out), "PASS", "the child run must report PASS")
	require.Contains(t, string(out), "SEC02-REFUSAL:",
		"the child must have printed the refusal it received")
	require.Contains(t, string(out), "tools.allow_write_paths",
		"the §3.2 refusal must name the overlapping field")
}

func sec02Child(t *testing.T) {
	api, _, _ := newSearchSettingsAPI(t)

	// A registered LibreOffice the published configuration knows nothing
	// about; the §3.2 check reads the registry fresh at check time.
	root := filepath.Join(t.TempDir(), "office")
	officepreview.TrustedOffice(t, api.homePath, root)
	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	entry := officeAllowancePattern(resolved)

	path := api.configPath()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk), "instrument: the fixture config must be valid JSON")
	require.NoError(t, officeAllowWriteMutate(disk, entry))
	seeded, err := json.Marshal(disk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, seeded, 0o600))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(before), entry, "instrument: the overlap really sits on disk")

	searchDef, ok := config.SearchProviderDefByID("tavily")
	require.True(t, ok, "BLOCKED: tavily search provider def missing — required by the removal route (config.SearchProviderDefByID)")

	// The real route: persistSearchKeyRemoval under configMu, exactly as
	// handleSearchKeyRemoval drives it.
	type removalOutcome struct{ err error }
	done := make(chan removalOutcome, 1)
	go func() {
		api.configMu.Lock()
		defer api.configMu.Unlock()
		_, removalErr := api.persistSearchKeyRemoval(searchDef, &searchKeyRemovalState{})
		done <- removalOutcome{err: removalErr}
	}()
	var removalErr error
	select {
	case out := <-done:
		removalErr = out.err
	case <-time.After(10 * time.Second):
		// The deadlock: exit at once so no cleanup waits on the leaked write
		// lock. The parent turns this marker into the RED.
		fmt.Println("SEC02-DEADLOCK")
		fmt.Println("the removal did not return within 10s: MutateConfig holds the loop write lock while the §3.2 check re-enters it via GetConfig (sec02-root-cause.md)")
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		fmt.Println(string(buf[:n]))
		os.Exit(3)
	}
	require.Error(t, removalErr,
		"SEC-OFFICE-02: the §3.2 check must still run on the removal path and refuse the new overlap, not skip it")
	fmt.Println("SEC02-REFUSAL: " + removalErr.Error())
	assert.Equal(t,
		officepreview.ConfigEntryRefusal("tools.allow_write_paths", entry, "LibreOffice", resolved),
		removalErr.Error(),
		"the removal must carry the exact §3.2 refusal, not a generic failure")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused removal must not write config.json")
	requireSavedSearchKey(t, api)
}

// sec02Evidence narrows the child output to the marker and the parked lock
// chain, so the saved RED log carries the deadlock evidence, not the whole
// dump.
func sec02Evidence(child string) string {
	var out []string
	for _, line := range strings.Split(child, "\n") {
		if strings.Contains(line, "SEC02-DEADLOCK") ||
			strings.Contains(line, "removal did not return") ||
			strings.Contains(line, "GetConfig") ||
			strings.Contains(line, "refuseNewProgramAccess") ||
			strings.Contains(line, "writeConfigJSONLocked") ||
			strings.Contains(line, "MutateConfig") ||
			strings.Contains(line, "persistSearchKeyRemoval") ||
			strings.Contains(line, "RWMutex.RLock") {
			out = append(out, line)
		}
		if len(out) >= 25 {
			break
		}
	}
	return strings.Join(out, "\n")
}
