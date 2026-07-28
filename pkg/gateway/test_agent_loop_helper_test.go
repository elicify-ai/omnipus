// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// mustAgentLoop is a gateway test helper that calls agent.NewAgentLoop and
// fatals on error. Reduces boilerplate after the return-type change (FR-029a).
//
// Registers t.Cleanup so background goroutines (retention sweepers, idle
// tickers, recap pipeline, browser manager) are shut down before t.TempDir()
// is removed. Without this, those goroutines occasionally write to the temp
// dir during teardown and TempDir's RemoveAll fails with
// "directory not empty", causing flaky failures under -count=N or parallel
// package runs.
//
// It also seeds ADR-046 P1 workspace membership (see
// ensureTestWorkspaceMembership) for every agent ID this cfg will register,
// mirroring pkg/agent/test_helpers_test.go's mustNewAgentLoop exactly. Without
// this, any pkg/gateway test that actually runs a real turn (not merely
// constructs an AgentLoop) hits runTurn's/runExternalCLISubTurn's shared
// resolveTurnWorkDirOrRefuse gate (pkg/agent/workspace_reroot.go) and the turn
// is refused with ErrAgentNotWorkspaceMember, because the test agent (almost
// always "main") is not a member of any workspace's CoreTeam.
func mustAgentLoop(
	t *testing.T,
	cfg *config.Config,
	msgBus *bus.MessageBus,
	provider providers.LLMProvider,
) *agent.AgentLoop {
	t.Helper()
	ensureTestWorkspaceMembership(t, cfg)
	al, err := agent.NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		t.Fatalf("agent.NewAgentLoop: %v", err)
	}
	t.Cleanup(al.Close)
	return al
}

// mustAgentLoopNoWorkspaceSeed is mustAgentLoop WITHOUT the ADR-046 P1
// workspace-membership auto-seed. Use it ONLY in tests that manage workspace
// membership themselves AND assert on the resolved working directory — e.g. the
// executor smoke tests, which drive a FAKE runner (capturingFakeDriver) and so
// never reach runTurn's/runExternalCLISubTurn's membership-refusal gate, but DO
// assert exactly which workspace/agent-home dir the executor resolves to.
// Auto-seeding a gateway-test-harness-default workspace would pollute that
// resolution (adding a second, lexicographically-first CoreTeam membership), so
// these tests must opt out.
func mustAgentLoopNoWorkspaceSeed(
	t *testing.T,
	cfg *config.Config,
	msgBus *bus.MessageBus,
	provider providers.LLMProvider,
) *agent.AgentLoop {
	t.Helper()
	al, err := agent.NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		t.Fatalf("agent.NewAgentLoop: %v", err)
	}
	t.Cleanup(al.Close)
	return al
}

// testHarnessWorkspaceMembershipID is the fixed workspace id the shared
// harness seed file uses for pkg/gateway tests. Kept distinct from any id an
// individual test creates itself (e.g. rest_workspaces_test.go's own
// writeWorkspaceFile calls) so the two never collide.
const testHarnessWorkspaceMembershipID = "gateway-test-harness-default"

// testHarnessWorkspaceMu serializes read-merge-write access to the shared
// harness seed file across concurrently running (t.Parallel) tests within
// this package's test binary. Without this, two goroutines racing a
// read-then-write on the same file could each drop the other's addition.
var testHarnessWorkspaceMu sync.Mutex

// ensureTestWorkspaceMembership makes every agent ID testHarnessAgentIDs(cfg)
// returns a member of SOME workspace at the CURRENT effective
// config.OmnipusHomeDir() (whichever of the real ambient $HOME/.omnipus, or
// an individual test's own t.Setenv("OMNIPUS_HOME", ...) override, is active
// for the calling test), so runTurn's/runExternalCLISubTurn's ADR-046 P1
// membership-refusal gate does not fire for a test that never set up a
// workspace of its own.
//
// Deliberately reads config.OmnipusHomeDir() (a pure, non-mutating function
// call) — NEVER t.Setenv — so this never disturbs a test's own OMNIPUS_HOME
// setup (rest_executor_smoketest_test.go and friends already set it
// explicitly to their own isolated temp dir before calling mustAgentLoop; in
// that case this seeds into THAT dir, not the ambient one). Mirrors
// pkg/agent/test_helpers_test.go's ensureTestWorkspaceMembership /
// seedTestWorkspaceMembershipForIDs exactly, adapted to reuse pkg/gateway's
// own production readWorkspaceFile/writeWorkspaceFile helpers (available here
// without the import-cycle risk pkg/agent's doc comment calls out, since
// pkg/gateway already depends on pkg/workspace directly).
//
// IDs already covered by an EXISTING workspace file (e.g. a test that wrote
// its own dedicated workspace JSON before calling mustAgentLoop) are left
// alone — this never double-seeds or contends with a test's own explicit
// setup. Uncovered IDs are unioned into one shared, fixed-id workspace file
// (testHarnessWorkspaceMembershipID), written atomically under a
// package-level mutex so concurrent callers never race a read-merge-write
// against each other or produce a torn read for a concurrently-running
// turn's own FindForAgentPreferring lookup.
func ensureTestWorkspaceMembership(t *testing.T, cfg *config.Config) {
	t.Helper()
	seedTestWorkspaceMembershipForIDs(t, testHarnessAgentIDs(cfg))
}

// seedTestWorkspaceMembershipForIDs is the primitive ensureTestWorkspaceMembership
// delegates to. A test that registers an agent OUTSIDE cfg.Agents.List, AFTER
// mustAgentLoop has already returned, can call this directly with that id —
// mirroring pkg/agent/test_helpers_test.go's second use case for the same
// primitive.
func seedTestWorkspaceMembershipForIDs(t *testing.T, ids []string) {
	t.Helper()
	home := config.OmnipusHomeDir()

	testHarnessWorkspaceMu.Lock()
	defer testHarnessWorkspaceMu.Unlock()

	var toSeed []string
	for _, id := range ids {
		if _, found := workspace.FindForAgent(home, id); !found {
			toSeed = append(toSeed, id)
		}
	}
	if len(toSeed) == 0 {
		return
	}

	rec, err := readWorkspaceFile(home, testHarnessWorkspaceMembershipID)
	if err != nil {
		if !errors.Is(err, errWorkspaceNotFound) {
			t.Fatalf("seedTestWorkspaceMembershipForIDs: read %s: %v", testHarnessWorkspaceMembershipID, err)
		}
		rec = storedWorkspace{
			ID:     testHarnessWorkspaceMembershipID,
			Name:   testHarnessWorkspaceMembershipID,
			Status: "active",
		}
	}

	seen := make(map[string]bool, len(rec.CoreTeam))
	for _, id := range rec.CoreTeam {
		seen[id] = true
	}
	for _, id := range toSeed {
		if !seen[id] {
			rec.CoreTeam = append(rec.CoreTeam, id)
			seen[id] = true
		}
	}
	rec.ID = testHarnessWorkspaceMembershipID

	if err := writeWorkspaceFile(home, rec); err != nil {
		t.Fatalf("seedTestWorkspaceMembershipForIDs: write %s: %v", testHarnessWorkspaceMembershipID, err)
	}
}

// testHarnessAgentIDs returns the set of agent IDs mustAgentLoop's caller's
// cfg will register with NewAgentRegistry: agent.DefaultAgentID ("main",
// always registered as the synthetic default/fallback agent regardless of
// cfg) plus every explicit, non-reserved ID in cfg.Agents.List.
//
// Mirrors pkg/agent/test_helpers_test.go's testHarnessAgentIDs exactly.
func testHarnessAgentIDs(cfg *config.Config) []string {
	ids := []string{agent.DefaultAgentID}
	if cfg == nil {
		return ids
	}
	seen := map[string]bool{agent.DefaultAgentID: true}
	for _, a := range cfg.Agents.List {
		if a.ID == "" || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		ids = append(ids, a.ID)
	}
	return ids
}
