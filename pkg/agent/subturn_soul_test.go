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
