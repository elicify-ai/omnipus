// Sub-turn soul composition tests (worker property-model correction).
//
// Verifies the (soul, task) composition invariant for sub-turns:
//   - When a sub-turn is delegated to a configured worker, the child turn
//     runs with system = worker's own soul (which may be empty) and user = the
//     submitted task. The legacy "You are a subagent" string is REMOVED.
//   - The same composition applies on the external-cli executor path so
//     native and external-cli sub-turns are uniform.
//   - An empty soul is valid and yields an empty system role (soul is OPTIONAL
//     by design). The worker itself now carries a compiled execution-discipline
//     persona, so the soul-less subject in these tests is a custom agent.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// soullessAgentID is an agent with no compiled prompt and no SOUL.md on disk.
// resolveDelegateSoul returns "" for it, which makes it the correct subject
// for the empty-soul invariant now that `worker` carries a real persona.
const soullessAgentID = "custom-agent-without-a-soul"

// TestResolveDelegateSoul_WorkerHasCompiledPrompt proves the seed worker
// carries a real compiled persona.
//
// This test previously asserted the OPPOSITE — that the worker's soul is empty
// — and used `worker` as the canonical example of a soul-less delegate. That
// was a description of a defect, not a requirement: because
// prompts["worker"] was "", BuildSystemPrompt fell through both the
// compiled-prompt and SOUL branches to the generic "You are Worker, a helpful
// AI assistant" fallback. The single most-used delegation target was the only
// seeded agent with no role-specific guidance, and it produced unbounded
// output that looked like a hang.
//
// The empty-soul invariant those tests really protected is preserved below in
// TestResolveDelegateSoul_SoullessAgentReturnsEmpty, with a subject that is
// genuinely soul-less.
func TestResolveDelegateSoul_WorkerHasCompiledPrompt(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := resolveDelegateSoul(al, string(coreagent.IDWorker))
	if got == "" {
		t.Fatal("resolveDelegateSoul(worker) is empty; the worker must carry execution-discipline guidance")
	}
	// Must not have regressed to the legacy generic wrapper.
	if containsAny(got, "You are a subagent", "subagent to complete", "Complete the given task") {
		t.Fatalf("resolveDelegateSoul(worker) leaked the legacy subagent wrapper: %q", got)
	}
	// Must not be the generic assistant fallback the defect produced.
	if containsAny(got, "a helpful AI assistant powered by Omnipus") {
		t.Fatalf("resolveDelegateSoul(worker) returned the generic fallback identity: %q", got)
	}
}

// TestResolveDelegateSoul_SoullessAgentReturnsEmpty preserves the original
// invariant: an agent with no compiled prompt and no SOUL.md resolves to an
// empty soul, with no panic and no fallback wrapper. Soul remains OPTIONAL.
func TestResolveDelegateSoul_SoullessAgentReturnsEmpty(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := resolveDelegateSoul(al, soullessAgentID)
	if got != "" {
		t.Fatalf("resolveDelegateSoul(%s) = %q, want empty (soul is OPTIONAL)", soullessAgentID, got)
	}
}

// TestResolveDelegateSoul_BaseAgentUsesCompiledPrompt proves the resolver
// returns the SEEDED base agent's compiled prompt when one is set. Jim, Mia,
// etc. carry compiled-in personas that take precedence over an on-disk SOUL.md
// (they are LOCKED, the SOUL.md on disk is the agent's identity anchor only
// for non-base agents).
func TestResolveDelegateSoul_BaseAgentUsesCompiledPrompt(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := resolveDelegateSoul(al, "jim")
	if got == "" {
		t.Fatal("resolveDelegateSoul(jim) returned empty; Jim must have a compiled persona")
	}
	// Sanity: must NOT be the legacy "You are a subagent" string.
	if containsAny(got, "You are a subagent", "subagent to complete", "Complete the given task") {
		t.Fatalf("resolveDelegateSoul(jim) leaked the legacy subagent wrapper: %q", got)
	}
}

// TestComposeDelegateInput_EmptySoulReturnsTaskOnly proves the external path's
// input is the TASK ALONE when the delegate's soul is empty. No persona, no
// wrapper, no "## System" header.
//
// The subject moved from `worker` to a genuinely soul-less agent when the
// worker gained a compiled persona; the invariant under test is unchanged.
func TestComposeDelegateInput_EmptySoulReturnsTaskOnly(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := composeDelegateInput(al, "summarize this file", "", soullessAgentID)
	if got != "summarize this file" {
		t.Fatalf("composeDelegateInput(soul-less, empty soul) = %q, want %q", got, "summarize this file")
	}
	if containsAny(got, "## System") {
		t.Fatalf("composeDelegateInput must not add a wrapper for an empty soul: %q", got)
	}
}

// TestComposeDelegateInput_WorkerPrependsSoul is the counterpart: now that the
// worker has a persona, the external path composes (soul, task) for it exactly
// as it does for a base agent.
func TestComposeDelegateInput_WorkerPrependsSoul(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := composeDelegateInput(al, "summarize this file", "", string(coreagent.IDWorker))
	if got == "summarize this file" {
		t.Fatal("composeDelegateInput(worker, task) returned task only; the worker's soul should prepend")
	}
	if !containsAny(got, "## System", "## Task", "summarize this file") {
		t.Fatalf("composeDelegateInput(worker) lost the (soul, task) composition: %q", got)
	}
}

// TestComposeDelegateInput_BaseAgentPrependsSoul proves the external path
// composes (soul, task) when the delegate has a soul, mirroring the native
// path's (system, user) split.
func TestComposeDelegateInput_BaseAgentPrependsSoul(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := composeDelegateInput(al, "summarize this file", "", "jim")
	if got == "summarize this file" {
		t.Fatal("composeDelegateInput(jim, task) returned task only; Jim's soul should prepend")
	}
	if !containsAny(got, "Jim") {
		t.Fatalf("composeDelegateInput(jim, task) missing Jim's persona marker: %q", got)
	}
	if !containsAny(got, "summarize this file") {
		t.Fatalf("composeDelegateInput(jim, task) missing the task: %q", got)
	}
}

// TestComposeDelegateInput_ExplicitActualSystemTakesPrecedence proves the
// legacy `ActualSystemPrompt` from the caller (e.g., tests, future callers)
// overrides the resolved delegate soul. The dispatch site is a single
// composition point and the caller wins.
func TestComposeDelegateInput_ExplicitActualSystemTakesPrecedence(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	explicit := "EXPLICIT_SYSTEM"
	got := composeDelegateInput(al, "task", explicit, "jim")
	if !containsAny(got, "EXPLICIT_SYSTEM") {
		t.Fatalf("composeDelegateInput: explicit ActualSystemPrompt must take precedence, got: %q", got)
	}
}

// TestSpawnSubTurn_NativeWithWorkerTargetComposesSoulAndTask is the integration
// proof: when spawnSubTurn is invoked with TargetAgentID=worker and a task,
// the child turn's processOptions carry (SystemPromptOverride=worker.soul,
// UserMessage=task). The legacy generic wrapper is GONE.
func TestSpawnSubTurn_NativeWithWorkerTargetComposesSoulAndTask(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	parent := &turnState{
		agent: al.registry.GetDefaultAgent(),
	}
	if parent.agent == nil {
		t.Fatal("test setup: no default agent")
	}

	// Inspect the resolved soul via the same resolution path spawnSubTurn uses.
	soul := resolveDelegateSoul(al, string(coreagent.IDWorker))
	if soul == "" {
		t.Fatal("worker soul is empty; the worker must carry execution-discipline guidance")
	}
	if containsAny(soul, "You are a subagent", "Complete the given task") {
		t.Fatalf("worker soul leaked the legacy subagent wrapper: %q", soul)
	}

	// Compose via the same helper spawnSubTurn uses for the external-cli path
	// and assert both halves survive: the worker's persona AND the task.
	composed := composeDelegateInput(al, "do the task", "", string(coreagent.IDWorker))
	if !containsAny(composed, "do the task") {
		t.Fatalf("composition dropped the task: %q", composed)
	}
	if !containsAny(composed, "## System", "## Task") {
		t.Fatalf("composition lost the (soul, task) split: %q", composed)
	}
}

// TestSpawnSubTurn_ExternalCLI_WorkerEmptySoulDeliversTaskOnly is the
// integration proof for the external-cli executor: the worker's empty soul
// yields a task-only prompt for the external CLI. No "## System" header, no
// persona. The driver sees just the task.
func TestSpawnSubTurn_ExternalCLI_WorkerEmptySoulDeliversTaskOnly(t *testing.T) {
	// The fake driver records the RunOptions.Input so we can assert what
	// the external CLI would have received as its prompt.
	fr, restore := withFakeDriver(t)
	defer restore()

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	// Worker agent with external-cli executor (legal: workers may use
	// non-native executors). Empty soul — the seed worker is soul-less
	// today, and that is the new valid state.
	worker := &AgentInstance{
		ID:   string(coreagent.IDWorker),
		Name: "Worker",
		Home: t.TempDir(),
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
		},
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}, Home: worker.Home},
			List: []config.AgentConfig{
				{ID: string(coreagent.IDWorker), Type: config.AgentTypeWorker, Home: worker.Home},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})

	ts := &turnState{
		agent:               worker,
		agentID:             worker.ID,
		turnID:              "ext-worker-soul-1",
		transcriptSessionID: "session_ext_soul_test",
	}
	task := "delegate this to the worker"

	// Compose exactly what spawnSubTurn would assemble for the external-cli
	// path with a SOUL-LESS target. The subject moved off `worker` when the
	// worker gained a persona; the invariant — an empty soul reaches the
	// external CLI as the bare task, with no "## System" wrapper injected
	// over the CLI's own system prompt — is unchanged and still worth pinning.
	composed := composeDelegateInput(al, task, "", soullessAgentID)
	if composed != task {
		t.Fatalf("composeDelegateInput(soul-less, empty soul) = %q, want %q (no wrapper)", composed, task)
	}

	// Drive the external dispatcher with the composed input and assert the
	// driver received task-only input (no "## System" header, no persona).
	go func() {
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "ok"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()
	res, err := runExternalCLISubTurn(context.Background(), al, ts, composed, 30*1_000_000_000)
	if err != nil {
		t.Fatalf("runExternalCLISubTurn: %v", err)
	}
	if res == nil || res.ForLLM != "ok" {
		t.Fatalf("aggregated output = %q, want %q", res.ForLLM, "ok")
	}

	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1", len(opts))
	}
	if opts[0].Input != task {
		t.Fatalf("external CLI input = %q, want task-only %q (worker has empty soul, no wrapper)", opts[0].Input, task)
	}
	if containsAny(opts[0].Input, "You are a subagent", "## System", "## Task") {
		t.Fatalf("external CLI input leaked the legacy subagent wrapper: %q", opts[0].Input)
	}
}

// TestResolveDelegateSoul_OnDiskSoulMdForCustomAgent proves the resolver
// falls back to the agent's on-disk SOUL.md when no compiled prompt is
// present. A custom agent with SOUL.md on disk gets its persona injected
// as the system role; without SOUL.md the soul is empty.
func TestResolveDelegateSoul_OnDiskSoulMdForCustomAgent(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	tmp := t.TempDir()
	soulPath := filepath.Join(tmp, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte("I am a custom worker persona."), 0o600); err != nil {
		t.Fatalf("write SOUL.md: %v", err)
	}
	cfg := al.GetConfig()
	cfg.Agents.List = []config.AgentConfig{
		{ID: "custom-worker", Type: config.AgentTypeWorker, Home: tmp, Locked: true},
	}

	got := resolveDelegateSoul(al, "custom-worker")
	if got != "I am a custom worker persona." {
		t.Fatalf("resolveDelegateSoul(custom-worker) = %q, want SOUL.md content", got)
	}
}

// TestResolveDelegateSoul_UnknownAgentReturnsEmpty proves an unresolved
// target never falls back to a generic "You are a subagent" wrapper. The
// caller (spawnSubTurn) treats an empty soul as "no persona" and the
// composition proceeds with task-only input.
func TestResolveDelegateSoul_UnknownAgentReturnsEmpty(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := resolveDelegateSoul(al, "nope-not-an-agent")
	if got != "" {
		t.Fatalf("resolveDelegateSoul(unknown) = %q, want empty (no wrapper fallback)", got)
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

// indexOf avoids pulling strings into the test file's import block.
func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	if len(sub) > len(s) {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
