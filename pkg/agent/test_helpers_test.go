// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
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
	// Pin the REAL Go caches before HOME is redirected. GOMODCACHE and GOCACHE
	// both DEFAULT to paths under $HOME, so pointing HOME at a fresh temp dir
	// silently aims every child `go` invocation in this package at an EMPTY
	// module cache — it then re-downloads the entire dependency tree on every
	// run. That is what made TestInterruptScope_RequiredByCompiler's nested
	// `go build` appear to hang for 13-17 minutes and time out the whole
	// package: it was not a deadlock, and not (only) a build-tag cache-key
	// mismatch, it was a cache that did not exist. Resolving these BEFORE the
	// Setenv keeps child builds on the warm shared caches while the test
	// process itself still gets an isolated HOME.
	for _, key := range []string{"GOMODCACHE", "GOCACHE"} {
		if os.Getenv(key) != "" {
			continue // already pinned explicitly by the caller/CI
		}
		out, envErr := exec.Command("go", "env", key).Output()
		if envErr != nil {
			continue // leave the default; a child build may be slow but correct
		}
		if v := strings.TrimSpace(string(out)); v != "" {
			if err := os.Setenv(key, v); err != nil {
				panic("pkg/agent TestMain: Setenv(" + key + "): " + err.Error())
			}
		}
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
	ensureTestIsolatedAgentHome(t, cfg)
	ensureTestWorkspaceMembership(t, cfg)
	al, err := NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	return al
}

// ensureTestIsolatedAgentHome gives cfg its own private, per-test home
// directory (via t.TempDir()) whenever the caller left Agents.Defaults.Home
// unset — closing a real (test-only) defect class this package's tests were
// silently hitting.
//
// The mechanism: config.Config.AgentHomeBasePath() (pkg/config/config.go) is
// expandHome(cfg.Agents.Defaults.Home), and THAT expandHome (unlike the
// same-named helper in pkg/agent/instance.go) does NOT fall back to
// $OMNIPUS_HOME/$HOME for an empty input — it returns "" verbatim. NewAgentLoop
// (pkg/agent/loop.go) then computes homePath := filepath.Dir(cfg.
// AgentHomeBasePath()); filepath.Dir("") returns ".", the test PROCESS's
// current working directory — this package's own source directory when
// running `go test ./pkg/agent/` — and roots the shared session store AND
// task store there (sharedDir := filepath.Join(homePath, "sessions"), etc.).
//
// Real gateway boots never hit this: config.DefaultConfig() (pkg/config/
// defaults.go) always seeds Agents.Defaults.Home from OmnipusHomeDir(), so
// homePath is never "." in production. But a large fraction of this
// package's tests construct a bare config.Config{} literal (bypassing
// DefaultConfig() entirely) and leave Agents.Defaults.Home empty — every one
// of those was, before this fix, silently writing real session directories
// straight into pkg/agent/sessions/ on the actual checkout: not under any
// temp root, never cleaned up between runs, accumulating indefinitely.
//
// Most such tests never noticed because they mint a fresh random session id
// every run (al.generateSubTurnID()/store.NewSession's ULID), so the leak was
// silent — a new, never-colliding directory each time. The exception that
// surfaced this: TestDelegateCancelHard/Soft_RealSubTurn_
// ActuallyCancelsTargetContext (interrupt_by_session_key_test.go, #577) use a
// FIXED, hardcoded DelegateSessionID ("test-577-delegate-child"[-soft]) so
// their SECOND (and every subsequent) run collided with their OWN first run's
// leftover directory and tripped CreateSessionWithID's FR-096 collision guard
// (pkg/session/unified_api.go) — surfacing as "target sub-turn's LLM call
// never started" (spawnSubTurn returns the collision error before ever
// reaching runTurn/the provider Chat call), which is a test-infrastructure
// artifact, NOT a defect in the delegate-cancel logic those tests exist to
// prove. Root-caused and verified via targeted instrumentation: the isolated
// failure is a ~10ms error return (session already exists), not a hang —
// indistinguishable from a genuine 5s timeout only because both tests discard
// spawnSubTurn's return value.
//
// t.TempDir() (not t.Setenv) is deliberate: safe to call even after
// t.Parallel(), auto-removed after the test, and unique per call — so this
// closes the leak for every mustNewAgentLoop caller in this package in one
// place, rather than requiring each test to remember to set Home itself (many
// already do, e.g. loop_test.go/loop_sysagent_test.go's own explicit
// `cfg.Agents.Defaults.Home = tmpDir`; this is a no-op for those — only a cfg
// that left Home empty is touched).
func ensureTestIsolatedAgentHome(t *testing.T, cfg *config.Config) {
	t.Helper()
	if cfg == nil {
		return
	}
	if cfg.Agents.Defaults.Home == "" {
		cfg.Agents.Defaults.Home = t.TempDir()
	}
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
//
// Every id passed in MUST already be normalized exactly the way the agent
// registry itself will key it (routing.NormalizeAgentID) — callers seed
// CoreTeam membership FOR that exact string, and resolveTurnWorkDirOrRefuse's
// FindForAgentPreferring/FindForAgent lookup at real-turn time is an EXACT
// string match against it (pkg/workspace/find_for_agent.go), never
// case-folded or re-normalized on the read side. testHarnessAgentIDs is the
// primary caller and already normalizes; a caller that passes a raw,
// un-normalized id (e.g. a mixed-case agent name straight from
// cfg.Agents.List) will fail the post-seed guard below LOUDLY instead of
// silently seeding a key nothing will ever look up.
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
	if len(toSeed) > 0 {
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

	// GUARD: this is the loud failure this class of bug needs. Re-resolve
	// every id via the EXACT same primitive (workspace.FindForAgent) the
	// ADR-046 P1 gate calls at real-turn time (resolveTurnWorkDirOrRefuse,
	// pkg/agent/workspace_reroot.go) — deliberately checking
	// routing.NormalizeAgentID(id), the REAL key the agent registry and every
	// AgentInstance.ID actually resolve to (registry.go:109, instance.go:160),
	// NOT the raw id the caller passed in. Checking the raw id would only
	// prove seeding-round-tripped-itself (it always does — we just wrote that
	// exact string into CoreTeam above) and would NOT catch this bug's actual
	// shape: a caller (e.g. a regressed testHarnessAgentIDs) seeding CoreTeam
	// with an un-normalized id that the registry will never look up under.
	// Re-deriving the expected key independently, from the same production
	// routing.NormalizeAgentID the registry itself calls, is what makes this a
	// real postcondition check instead of a tautology.
	//
	// If any id's normalized form still doesn't resolve to a workspace after
	// seeding, fail the test HERE, at setup — not later, as a real turn
	// silently refusing with nothing but a "turn refused: agent is not a
	// member of any workspace" WARN log that a green test suite never
	// surfaces. This is exactly the shape of bug this file shipped with:
	// testHarnessAgentIDs seeding an agent's raw (non-normalized) config ID
	// while the registry keys real turns off routing.NormalizeAgentID(ac.ID)
	// — an exact-string-match miss. Mirrors the identical guard added to
	// pkg/gateway/test_agent_loop_helper_test.go's copy of this helper.
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
// cfg will register with NewAgentRegistry: every explicit ID in
// cfg.Agents.List — INCLUDING an agent configured with an external-CLI
// executor (Subagents.Executor.Kind == config.ExecutorKindExternalCLI) —
// NORMALIZED exactly the way NewAgentRegistry itself keys the registry.
//
// There is no synthetic "main" sentinel added anymore. The retired "main"
// sentinel was never a modelled agent (no schema, no entity record, absent
// from cfg.Agents.List) yet NewAgentRegistry used to register it
// unconditionally regardless of cfg. That is gone: a cfg with an empty
// Agents.List now produces a registry with ZERO agents, and any test that
// needs a real turn to route somewhere must add its own explicit entry to
// cfg.Agents.List (e.g. {ID: "mia", Name: "Mia"}) rather than relying on an
// implicit fallback identity.
//
// registry.go's NewAgentRegistry never registers an agent under its raw
// cfg.Agents.List[i].ID: every entry is keyed under
// routing.NormalizeAgentID(ac.ID) (registry.go:109 — lower-cased, sanitized
// to [a-z0-9][a-z0-9_-]*), and instance.go's NewAgentInstance independently
// normalizes the SAME way when it sets AgentInstance.ID. A real turn's
// ADR-046 P1 gate (resolveTurnWorkDirOrRefuse) is then called with that
// normalized ts.agent.ID, and workspace.FindForAgent's CoreTeam lookup is an
// EXACT string match — so seeding CoreTeam with the raw, un-normalized
// config ID (e.g. a mixed-case agent name) writes a key nothing ever looks
// up. The turn still runs — it just silently refuses at the gate with a WARN
// log ("turn refused: agent is not a member of any workspace"), never a test
// failure, so any test that believed it exercised a real turn for such an
// agent did not. This mirrors the identical bug fixed in
// pkg/gateway/test_agent_loop_helper_test.go's copy of this helper — same
// shape, same fix (normalize before seeding).
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
	var ids []string
	if cfg == nil {
		return ids
	}
	seen := map[string]bool{}
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

// -----------------------------------------------------------------------------
// Race-free capture of the process-global slog default
// -----------------------------------------------------------------------------

// raceFreeLogBuffer is a mutex-protected sink for slog output.
//
// Any test that asserts on real emitted log bytes has to swap the
// PROCESS-GLOBAL slog default (slog.SetDefault) — production code under test
// logs through slog.Warn/slog.Error, not through an injectable logger. That
// makes the sink shared with every other goroutine alive in the test binary,
// and a bare bytes.Buffer is not safe for concurrent use, so
// Write/String/Reset from different goroutines is a genuine data race.
//
// This is not hypothetical and it is not confined to a test that spawns its
// own goroutines. Two independent CI failures have been caused by it:
//
//   - 2026-08-19: an async sub-turn kept logging after spawnSubTurn returned,
//     racing wave3_fix5b_test.go's Buffer.String() (this type's original home).
//   - 2026-08-23: pkg/session's stats-flusher goroutine — started per
//     UnifiedStore by NewAgentLoop and never stopped by the test that created
//     it — ticked every 5s (config.DefaultSessionStatsFlushInterval) and called
//     slog.Warn from pkg/session/unified_stats_flush.go's flushAllDirtyStats,
//     racing task_trigger_rrule_test.go's Buffer.Reset()/String(). The writer
//     belonged to a DIFFERENT, already-finished test; the reader belonged to
//     TestTriggerScheduler_UnreadableTaskEscalatesToError, which is what the
//     detector failed.
//
// The lesson from the second one: the racing writer need not be anything the
// asserting test started or knows about. Treat any global-slog capture as
// concurrently written by definition and always use this type — never a bare
// bytes.Buffer.
//
// The race is in the test harness, not in the code under test: the fix is to
// synchronize the sink, not to silence the detector.
type raceFreeLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *raceFreeLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *raceFreeLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Reset drops everything captured so far. Used by tests that assert on one
// phase, clear, then assert on the next.
func (b *raceFreeLogBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// captureDefaultSlog installs a text handler at level as the process-global
// slog default for the duration of the test and returns the race-free buffer
// it writes into, restoring the previous default via t.Cleanup.
func captureDefaultSlog(t *testing.T, level slog.Level) *raceFreeLogBuffer {
	t.Helper()
	buf := &raceFreeLogBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}
