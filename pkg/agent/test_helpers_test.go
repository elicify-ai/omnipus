// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestMain isolates every test in this package from the real developer/CI
// machine's actual $HOME. It sets HOME (deliberately NOT OMNIPUS_HOME) once,
// via plain os.Setenv, before any individual test runs.
//
// omnipusHome() (pkg/agent/instance.go) and getGlobalConfigDir()
// (pkg/agent/context.go) both check OMNIPUS_HOME first and only fall back to
// os.UserHomeDir() (== $HOME on Linux) when it is unset. A large fraction of
// this package's tests exercise a real turn without ever setting OMNIPUS_HOME
// themselves — runTurn's ADR-046 P1 workspace-membership gate
// (ErrAgentNotWorkspaceMember) resolves workspace.FindForAgentPreferring
// against omnipusHome(), so without this isolation every one of them would
// resolve against the real machine's home directory.
//
// This sets HOME, not OMNIPUS_HOME, specifically because several tests in
// this package deliberately exercise the "OMNIPUS_HOME unset, falls back to
// HOME" code path itself (instance_test.go's TestResolveAgentHome_OMNIPUSHome,
// context_cache_test.go's TestGlobalSkillFileContentChange /
// TestBuiltinSkillFileContentChange, runner_egress_test.go) via their own
// t.Setenv("HOME", ...) — setting OMNIPUS_HOME here globally would shadow
// that fallback for the whole binary and break every one of them. Setting
// HOME instead composes cleanly: each of those tests' own t.Setenv layers on
// top of (and is restored back to) this TestMain default for the duration of
// that one test, exactly as if the real machine's HOME had been this value
// all along.
//
// This also means ensureTestWorkspaceMembership (used by mustNewAgentLoop,
// below) never needs to call t.Setenv itself: several tests in this package
// call t.Parallel() and ALSO call mustNewAgentLoop for unrelated (non-turn)
// setup — e.g. cancel_test.go's newCancelTestAgentLoop helper — and
// t.Setenv panics on a test that has already called t.Parallel(). Resolving
// the effective home directory via a plain, non-mutating read (omnipusHome()
// itself) sidesteps that entirely.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "omnipus-agent-pkg-test-home-*")
	if err != nil {
		panic("pkg/agent TestMain: MkdirTemp: " + err.Error())
	}
	if err := os.Setenv("HOME", tmp); err != nil {
		panic("pkg/agent TestMain: Setenv(HOME): " + err.Error())
	}
	// No os.RemoveAll(tmp) here: os.Exit below (via m.Run()'s return code)
	// skips deferred calls, and the process is about to exit anyway — the
	// directory is under the OS temp root and cleaned up by the standard
	// temp-file lifecycle regardless.
	os.Exit(m.Run())
}

// mustNewAgentLoop is a test helper that calls NewAgentLoop and fatals on error.
// Use this in all _test.go files within this package to avoid repeating the
// error-handling boilerplate after the return-type change (FR-029a / Architect #7).
//
// It also seeds ADR-046 P1 workspace membership (see
// ensureTestWorkspaceMembership) for every agent ID this cfg will register,
// so the vast majority of this package's tests — which run a real turn
// without ever setting up a workspace themselves — are unaffected by
// runTurn's membership-refusal gate. A test that specifically wants to
// exercise the "member of no workspace" refusal path must call NewAgentLoop
// directly instead of this helper (see workspace_team_reroot_test.go's
// TestRunTurn_WorkspacelessAgentRefused).
func mustNewAgentLoop(
	t *testing.T,
	cfg *config.Config,
	msgBus *bus.MessageBus,
	provider providers.LLMProvider,
) *AgentLoop {
	t.Helper()
	ensureTestWorkspaceMembership(t, cfg)
	al, err := NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	return al
}

// testHarnessWorkspaceMembershipID is the fixed workspace id the shared
// harness seed file uses. Kept distinct from any id a test creates itself
// (e.g. workspace_team_reroot_test.go's "team-ws") so the two never collide.
const testHarnessWorkspaceMembershipID = "test-harness-default"

// testHarnessWorkspaceMu serializes read-merge-write access to the shared
// harness seed file across concurrently running (t.Parallel) tests within
// this package's test binary. Without this, two goroutines racing a
// read-then-write on the same file could each drop the other's addition.
var testHarnessWorkspaceMu sync.Mutex

// testHarnessWorkspaceRecord mirrors the minimal on-disk workspace shape
// pkg/workspace's FindForAgent/FindForAgentPreferring read (id + core_team) —
// the same raw-JSON approach workspace_team_reroot_test.go already uses for
// its own dedicated per-test workspace files. Kept minimal and package-local
// rather than importing pkg/gateway's storedWorkspace, which would risk an
// import cycle back into pkg/agent via the gateway's own AgentLoop wiring.
type testHarnessWorkspaceRecord struct {
	ID       string   `json:"id"`
	CoreTeam []string `json:"core_team"`
}

// ensureTestWorkspaceMembership makes every agent ID testHarnessAgentIDs(cfg)
// returns a member of SOME workspace at the CURRENT effective omnipusHome()
// (whichever of TestMain's HOME default, or an individual test's own
// t.Setenv("OMNIPUS_HOME"/"HOME", ...) override, is active for the calling
// test), so runTurn's ADR-046 P1 membership-refusal gate does not fire for a
// test that never set up a workspace of its own.
//
// Deliberately reads omnipusHome() (a pure, non-mutating function call) —
// NEVER t.Setenv — so this is safe to call from a test that has already
// called t.Parallel() (see TestMain's doc comment for why that matters).
//
// IDs already covered by an EXISTING workspace file (e.g. a test that wrote
// its own dedicated workspace JSON, like workspace_team_reroot_test.go's
// "team-ws", before calling mustNewAgentLoop) are left alone — this never
// double-seeds or contends with a test's own explicit setup, and never
// creates the ambiguous multi-membership case FindForAgent's own doc comment
// describes. Uncovered IDs are unioned into one shared, fixed-id workspace
// file (testHarnessWorkspaceMembershipID), written atomically under a
// package-level mutex so concurrent (t.Parallel) callers never race a
// read-merge-write against each other or produce a torn read for a
// concurrently-running turn's own FindForAgentPreferring lookup.
func ensureTestWorkspaceMembership(t *testing.T, cfg *config.Config) {
	t.Helper()
	seedTestWorkspaceMembershipForIDs(t, testHarnessAgentIDs(cfg))
}

// seedTestWorkspaceMembershipForIDs is the primitive ensureTestWorkspaceMembership
// delegates to. It also covers a second case ensureTestWorkspaceMembership
// cannot: a test that registers an agent OUTSIDE cfg.Agents.List, AFTER
// mustNewAgentLoop has already returned — e.g. process_scheduled_test.go's
// registerAgent helper, which stores directly into al.registry.agents for a
// dynamically-chosen owner id ("mia"/"max"/etc.) never present in the cfg
// mustNewAgentLoop saw. Such a test calls this directly, right after
// registering the id, with that id.
//
// Makes every id in ids a member of SOME workspace at the CURRENT effective
// omnipusHome(). Deliberately reads omnipusHome() (a pure, non-mutating
// function call) — NEVER t.Setenv — so this is safe to call from a test that
// has already called t.Parallel(). IDs already covered by an EXISTING
// workspace file are left alone (never double-seeds or contends with a
// test's own explicit setup); uncovered IDs are unioned into one shared,
// fixed-id workspace file (testHarnessWorkspaceMembershipID), written
// atomically under a package-level mutex so concurrent (t.Parallel) callers
// never race a read-merge-write against each other or produce a torn read
// for a concurrently-running turn's own FindForAgentPreferring lookup.
func seedTestWorkspaceMembershipForIDs(t *testing.T, ids []string) {
	t.Helper()
	home := omnipusHome()

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

	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seedTestWorkspaceMembershipForIDs: mkdir workspaces: %v", err)
	}
	path := filepath.Join(dir, testHarnessWorkspaceMembershipID+".json")

	rec := testHarnessWorkspaceRecord{ID: testHarnessWorkspaceMembershipID}
	if existing, rerr := os.ReadFile(path); rerr == nil {
		_ = json.Unmarshal(existing, &rec) // best-effort merge; a corrupt file is overwritten below
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

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("seedTestWorkspaceMembershipForIDs: marshal: %v", err)
	}
	if err := fileutil.WriteFileAtomic(path, data, 0o644); err != nil {
		t.Fatalf("seedTestWorkspaceMembershipForIDs: write %s: %v", path, err)
	}
}

// seedDistinctTestWorkspacesForIDs makes each id the SOLE member of its OWN
// dedicated workspace (id "test-harness-solo-<id>"), so every id resolves —
// via FindForAgentPreferring in resolveTurnWorkDirOrRefuse — to a DISTINCT
// workspace work/ directory.
//
// This is the deliberate counterpart to seedTestWorkspaceMembershipForIDs,
// which unions every id into ONE shared workspace (hence one shared work/ dir,
// one shared external_dispatch.go workspaceRunLocks entry). Under ADR-046 P1
// every dispatch's WorkDir is the acting agent's resolved workspace work/ dir
// (agent.Home no longer participates), so a concurrency test whose sub-turns
// must NOT serialize on that per-work-dir lock now expresses "distinct work
// dirs" as "distinct workspaces" rather than the pre-P1 "distinct agent.Home".
//
// Same discipline as seedTestWorkspaceMembershipForIDs: guarded by
// testHarnessWorkspaceMu, written atomically, and idempotent — an id already a
// member of SOME workspace is left untouched (never double-seeded into a
// second workspace, which would create FindForAgent's ambiguous
// multi-membership case).
func seedDistinctTestWorkspacesForIDs(t *testing.T, ids []string) {
	t.Helper()
	home := omnipusHome()

	testHarnessWorkspaceMu.Lock()
	defer testHarnessWorkspaceMu.Unlock()

	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seedDistinctTestWorkspacesForIDs: mkdir workspaces: %v", err)
	}
	for _, id := range ids {
		if _, found := workspace.FindForAgent(home, id); found {
			continue
		}
		wsID := "test-harness-solo-" + id
		rec := testHarnessWorkspaceRecord{ID: wsID, CoreTeam: []string{id}}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("seedDistinctTestWorkspacesForIDs: marshal %s: %v", id, err)
		}
		path := filepath.Join(dir, wsID+".json")
		if err := fileutil.WriteFileAtomic(path, data, 0o644); err != nil {
			t.Fatalf("seedDistinctTestWorkspacesForIDs: write %s: %v", path, err)
		}
	}
}

// testHarnessAgentIDs returns the set of agent IDs mustNewAgentLoop's caller's
// cfg will register with NewAgentRegistry: DefaultAgentID ("main", always
// registered as the synthetic default/fallback agent regardless of cfg — see
// NewAgentRegistry in registry.go) plus every explicit, non-reserved ID in
// cfg.Agents.List — INCLUDING an agent configured with an external-CLI
// executor (Subagents.Executor.Kind == config.ExecutorKindExternalCLI).
//
// External-CLI agents are NOT excluded (a prior version of this function
// excluded them): external_dispatch.go's runExternalCLISubTurn now calls the
// EXACT SAME shared gate (resolveTurnWorkDirOrRefuse, workspace_reroot.go)
// loop.go's runTurn does — a 7-reviewer gate found the prior asymmetry (the
// external-cli path silently falling through to agent.Home for a non-member)
// a BLOCK. So an external-CLI agent now needs seeded membership to execute,
// exactly like a native one — no exception by dispatch kind.
//
// A test whose whole point is asserting WHICH workspace a dispatch resolves
// to (subturn_target_identity_test.go's identity-source regression tests;
// task_executor_external_cli_test.go's
// TestProcessTaskDirect_ExternalCLIWorker_DispatchesViaExternalCLI) must NOT
// rely on this shared, catch-all seeding for the id(s) it cares about —
// every id this function seeds lands in the SAME shared workspace
// (testHarnessWorkspaceMembershipID), so two different agents seeded here
// are indistinguishable members of one workspace, which would mask a bug
// that resolves the wrong agent's identity (e.g. the parent's instead of the
// delegation target's) since both would still land in the same place. Such
// a test must instead persist its OWN dedicated workspace file (listing only
// the id(s) it cares about) BEFORE calling mustNewAgentLoop —
// seedTestWorkspaceMembershipForIDs skips any id workspace.FindForAgent
// already resolves, so a pre-existing dedicated file keeps that id out of
// the shared one and preserves the test's isolation.
//
// A test asserting an agent's OWN private per-agent directory (rather than
// a workspace's work/) — for either dispatch kind — is testing a premise
// ADR-046 P1 retired; see TestSpawnSubTurn_NativeDispatch_AdoptsTargetWorkspaceForFileTools,
// updated to assert workspace-relative resolution instead.
func testHarnessAgentIDs(cfg *config.Config) []string {
	ids := []string{DefaultAgentID}
	if cfg == nil {
		return ids
	}
	seen := map[string]bool{DefaultAgentID: true}
	for _, a := range cfg.Agents.List {
		if a.ID == "" || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		ids = append(ids, a.ID)
	}
	return ids
}
