// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ensureIsolatedTestOmnipusHome makes config.OmnipusHomeDir() (and pkg/agent's
// own equivalent, package-private omnipusHome() — both resolve OMNIPUS_HOME
// first) resolve to a throwaway per-test temp directory, UNLESS the calling
// test (or a helper it already called earlier in the SAME test) has set
// OMNIPUS_HOME itself — that explicit choice is always respected and never
// overridden, since several tests (e.g. rest_agent_executor_test.go's
// buildExecutorTestAPI, rest_executor_smoketest_test.go's
// newTestRestAPIWithWorkspaceAgent) set OMNIPUS_HOME to a SPECIFIC tmpDir they
// have already written config.json / entity-store / marker files into —
// forcing a second, different tmpDir here would orphan those files and break
// the test.
//
// Without this, a test that never sets OMNIPUS_HOME falls through to the
// developer's real $HOME/.omnipus for EVERY home-directory read that happens
// during mustAgentLoop/mustAgentLoopNoWorkspaceSeed — not just the workspace-
// membership seed file (seedTestWorkspaceMembershipForIDs), but also every
// session-store/credential/memory path pkg/agent's own NewAgentInstance
// resolves internally via omnipusHome() — silently reading and writing real
// user data on every test run. Called from BOTH mustAgentLoop and
// mustAgentLoopNoWorkspaceSeed for that reason: the no-seed variant still
// constructs a real agent.AgentLoop, so it still needs isolation even though
// it never calls ensureTestWorkspaceMembership.
func ensureIsolatedTestOmnipusHome(t *testing.T) {
	t.Helper()
	if os.Getenv("OMNIPUS_HOME") != "" {
		return
	}
	t.Setenv("OMNIPUS_HOME", t.TempDir())
}

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
	ensureIsolatedTestOmnipusHome(t)
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
	ensureIsolatedTestOmnipusHome(t)
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
// config.OmnipusHomeDir() (whichever of an individual test's own
// t.Setenv("OMNIPUS_HOME", ...) override, or mustAgentLoop's own
// ensureIsolatedTestOmnipusHome fallback isolation, is active for the calling
// test — never the real ambient $HOME/.omnipus; see ensureIsolatedTestOmnipusHome's
// doc comment), so runTurn's/runExternalCLISubTurn's ADR-046 P1
// membership-refusal gate does not fire for a test that never set up a
// workspace of its own.
//
// This function itself never calls t.Setenv — by the time it runs (always via
// mustAgentLoop, which calls ensureIsolatedTestOmnipusHome first), OMNIPUS_HOME
// is already guaranteed to be either the calling test's own explicit override
// or an isolated temp dir, so it never disturbs a test's own OMNIPUS_HOME
// setup (rest_executor_smoketest_test.go and friends already set it
// explicitly to their own isolated temp dir before calling mustAgentLoop; in
// that case this seeds into THAT dir, not the ambient one). Mirrors
// pkg/agent/test_helpers_test.go's ensureTestWorkspaceMembership /
// seedTestWorkspaceMembershipForIDs, adapted to reuse pkg/gateway's own
// production readWorkspaceFile/writeWorkspaceFile helpers (available here
// without the import-cycle risk pkg/agent's doc comment calls out, since
// pkg/gateway already depends on pkg/workspace directly) — and, unlike that
// file's version, normalizing every id the way the registry actually keys it
// (see testHarnessAgentIDs) and asserting the seed actually took (see
// seedTestWorkspaceMembershipForIDs's guard).
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
//
// Every id passed in MUST already be normalized exactly the way the agent
// registry itself will key it (routing.NormalizeAgentID) — callers seed
// CoreTeam membership FOR that exact string, and resolveTurnWorkDirOrRefuse's
// FindForAgentPreferring/FindForAgent lookup at real-turn time is an EXACT
// string match against it (pkg/workspace/find_for_agent.go), never
// case-folded or re-normalized on the read side. testHarnessAgentIDs is the
// only production caller and already normalizes; a caller that passes a raw,
// un-normalized id (e.g. a mixed-case ULID straight from cfg.Agents.List)
// will fail the post-seed guard below LOUDLY instead of silently seeding a
// key nothing will ever look up.
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
	if len(toSeed) > 0 {
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

	// GUARD: this is the loud failure this class of bug needs. Re-resolve
	// every id via the EXACT same primitive (workspace.FindForAgent) the
	// ADR-046 P1 gate calls at real-turn time (resolveTurnWorkDirOrRefuse,
	// pkg/agent/workspace_reroot.go) — deliberately checking
	// routing.NormalizeAgentID(id), the REAL key the agent registry and every
	// AgentInstance.ID actually resolve to (registry.go:109,
	// instance.go:160), NOT the raw id the caller passed in. Checking the raw
	// id would only prove seeding-round-tripped-itself (it always does — we
	// just wrote that exact string into CoreTeam above) and would NOT catch
	// this bug's actual shape: a caller (e.g. a regressed testHarnessAgentIDs)
	// seeding CoreTeam with an un-normalized id that the registry will never
	// look up under. Re-deriving the expected key independently, from the
	// same production routing.NormalizeAgentID the registry itself calls, is
	// what makes this a real postcondition check instead of a tautology.
	//
	// If any id's normalized form still doesn't resolve to a workspace after
	// seeding, fail the test HERE, at setup — not later, as a real turn
	// silently refusing with nothing but a "turn refused: agent is not a
	// member of any workspace" WARN log that a green test suite never
	// surfaces. This is exactly the shape of bug this file shipped with:
	// testHarnessAgentIDs seeding an agent's raw (non-normalized) config ID
	// while the registry keys real turns off routing.NormalizeAgentID(ac.ID)
	// — an exact-string-match miss.
	for _, id := range ids {
		normalized := routing.NormalizeAgentID(id)
		if _, found := workspace.FindForAgent(home, normalized); !found {
			t.Fatalf("seedTestWorkspaceMembershipForIDs: BLOCKED: agent_id=%q (registry key %q) "+
				"does not resolve to any workspace after seeding — any real turn for this agent "+
				"will silently refuse at the ADR-046 P1 gate (resolveTurnWorkDirOrRefuse) with "+
				"only a WARN log, not a test failure. The seeded id must equal "+
				"routing.NormalizeAgentID's output — the exact key the agent registry keys real "+
				"turns under.", id, normalized)
		}
	}
}

// testHarnessAgentIDs returns the set of agent IDs mustAgentLoop's caller's
// cfg will register with NewAgentRegistry: agent.DefaultAgentID ("main",
// always registered as the synthetic default/fallback agent regardless of
// cfg) plus every explicit, non-reserved ID in cfg.Agents.List — NORMALIZED
// exactly the way NewAgentRegistry itself keys the registry.
//
// pkg/agent/registry.go's NewAgentRegistry never registers an agent under its
// raw cfg.Agents.List[i].ID: every entry is keyed under
// routing.NormalizeAgentID(ac.ID) (registry.go:109 — lower-cased, sanitized
// to [a-z0-9][a-z0-9_-]*), and pkg/agent/instance.go's NewAgentInstance
// independently normalizes the SAME way when it sets AgentInstance.ID. A
// real turn's ADR-046 P1 gate (resolveTurnWorkDirOrRefuse) is then called
// with that normalized ts.agent.ID, and workspace.FindForAgent's CoreTeam
// lookup is an EXACT string match — so seeding CoreTeam with the raw,
// un-normalized config ID (e.g. an upper-case ULID like
// rest_plans_test.go's testPlansAgentID, "01JXTESTPLANSAGENT0000001")
// writes a key nothing ever looks up. The turn still runs — it just silently
// refuses at the gate with a WARN log ("turn refused: agent is not a member
// of any workspace"), never a test failure, so any gateway test that
// believed it exercised a real turn for such an agent did not. Confirmed
// pre-fix by rest_plans_test.go:876-882's manual, single-test workaround
// (`seedTestWorkspaceMembershipForIDs(t, []string{strings.ToLower(testPlansAgentID)})`),
// which is now redundant (harmless no-op) since this function normalizes for
// every caller.
//
// Mirrors pkg/agent/test_helpers_test.go's testHarnessAgentIDs — MINUS this
// normalization step, which that file's version does not have either (same
// latent bug, out of this package's fix scope; see report).
func testHarnessAgentIDs(cfg *config.Config) []string {
	ids := []string{agent.DefaultAgentID}
	if cfg == nil {
		return ids
	}
	seen := map[string]bool{agent.DefaultAgentID: true}
	for _, a := range cfg.Agents.List {
		if a.ID == "" {
			continue
		}
		id := routing.NormalizeAgentID(a.ID)
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}
