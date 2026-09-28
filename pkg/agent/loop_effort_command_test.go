// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_effort_command_test.go — RED tests for WP-H's backend half
// (thinking-reasoning-spec.md §9.5, §1 C5 clearing paragraph, §16 test 39,
// SC-015): the production wiring — /effort through al.handleCommand →
// buildCommandsRuntime → the conversation-effort callbacks — reaches the
// loop's own session store, stays isolated per conversation, leaves the
// agent model untouched (the §2.2 explicit non-path), survives a restart
// read, and a real model change (rt.SwitchModel → ApplyAgentModel, the path
// /model drives) clears the conversation's stored effort.
//
// Boundaries: real AgentLoop (mustNewAgentLoop), real UnifiedStore, real
// command execution; provider construction with unroutable API bases so no
// network is dialed. Expected values derive from the spec, never from
// observed implementation output.
//
// RED status at time of writing: SessionMeta.ReasoningEffort,
// Runtime.GetConversationEffort/SetConversationEffort and the registered
// "effort" command DO NOT EXIST yet — this file is expected to fail to
// compile, naming them verbatim. That compile failure is the RED evidence.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// newEffortAgentLoop builds a real AgentLoop whose default agent resolves
// two models (gpt-4.1 primary, deepseek-chat switch target) so the /model
// path (rt.SwitchModel → ApplyAgentModel) can be driven for real. Provider
// construction only — API bases are unroutable, nothing dials.
func newEffortAgentLoop(t *testing.T) (*AgentLoop, *AgentInstance, *session.UnifiedStore) {
	t.Helper()
	t.Setenv("LOOP_APPLY_LOCAL_KEY", "local-key")
	t.Setenv("LOOP_APPLY_REMOTE_KEY", "remote-key")

	tmpDir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Provider: "openai", Model: "gpt-4.1"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		Providers: []*config.ModelConfig{
			{
				Provider:  "openai",
				Model:     "gpt-4.1",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "LOOP_APPLY_LOCAL_KEY",
			},
			{
				Provider:  "deepseek",
				Model:     "deepseek-chat",
				APIBase:   "http://127.0.0.1:0",
				APIKeyRef: "LOOP_APPLY_REMOTE_KEY",
			},
		},
	}
	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent from the effort fixture")
	}
	store := al.GetAgentStore(agent.ID)
	if store == nil {
		t.Fatalf("GetAgentStore(%q) = nil — the effort tests need the loop's UnifiedStore", agent.ID)
	}
	return al, agent, store
}

// runEffortCommand drives one slash command through the REAL dispatch path
// (handleCommand → buildCommandsRuntime → executor) and returns the reply.
func runEffortCommand(t *testing.T, al *AgentLoop, agent *AgentInstance, opts *processOptions, text string) string {
	t.Helper()
	reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{
		Channel: "cli",
		Sender:  bus.SenderInfo{CanonicalID: "user-effort"},
		ChatID:  "chat-effort",
		Content: text,
	}, agent, opts)
	if !handled {
		t.Fatalf("%q: handleCommand reported handled=false — a registered builtin must be handled", text)
	}
	return reply
}

// TestHandleCommand_Effort_WiresConversationCallbacksToTheLoopSessionStore
// (spec §9.5: "/effort and /effort <level> read or patch
// SessionMeta.ReasoningEffort for commands.Runtime.SessionID through new
// conversation-effort callbacks on Runtime"; SC-015 restart/isolation
// clauses; test 39's "A changes, B and agent config do not") — driven end to
// end through the real dispatch path, asserting the loop's own session
// store.
func TestHandleCommand_Effort_WiresConversationCallbacksToTheLoopSessionStore(t *testing.T) {
	al, agent, store := newEffortAgentLoop(t)

	// Two real channel conversations of the SAME agent in the loop's own store.
	a, err := store.NewChannelSession("telegram", "tg-instance", "peer-a", agent.ID, "A")
	if err != nil {
		t.Fatalf("NewChannelSession A: %v", err)
	}
	b, err := store.NewChannelSession("telegram", "tg-instance", "peer-b", agent.ID, "B")
	if err != nil {
		t.Fatalf("NewChannelSession B: %v", err)
	}
	modelBefore := agent.Model

	// A sets high.
	reply := runEffortCommand(t, al, agent, &processOptions{SessionKey: a.ID}, "/effort high")
	if !strings.Contains(reply, "high") {
		t.Fatalf("/effort high reply=%q, want it to confirm the new level", reply)
	}

	// A reads it back.
	reply = runEffortCommand(t, al, agent, &processOptions{SessionKey: a.ID}, "/effort")
	if !strings.Contains(reply, "high") {
		t.Fatalf("/effort read reply=%q, want it to report 'high'", reply)
	}

	// B never saw it (D30 isolation).
	gotB, err := store.GetMeta(b.ID)
	if err != nil {
		t.Fatalf("GetMeta B: %v", err)
	}
	if gotB.ReasoningEffort != "" {
		t.Fatalf("conversation B stored=%q, want empty — /effort is isolated to its own conversation (D30)", gotB.ReasoningEffort)
	}

	// B reports Default.
	reply = runEffortCommand(t, al, agent, &processOptions{SessionKey: b.ID}, "/effort")
	if !strings.Contains(reply, "Default") {
		t.Fatalf("B's /effort reply=%q, want Default", reply)
	}

	// The agent model was never touched by /effort (the §2.2 explicit
	// non-path: /effort MUST NOT reach SwitchModel/ApplyAgentModel — a call
	// there would have flipped agent.Model off its fixture default).
	if agent.Model != modelBefore {
		t.Fatalf("agent.Model changed %q → %q during /effort operations — /effort must never route through the agent-config writer",
			modelBefore, agent.Model)
	}

	// Restart read: a fresh store over the same base dir (empty meta cache →
	// real disk reads) still sees A's value (SC-015: "survives restart in A").
	reopened, err := session.NewUnifiedStore(store.BaseDir())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	gotA, err := reopened.GetMeta(a.ID)
	if err != nil {
		t.Fatalf("reopen GetMeta A: %v", err)
	}
	if gotA.ReasoningEffort != "high" {
		t.Fatalf("after simulated restart, A stored=%q, want %q (SC-015 restart clause)", gotA.ReasoningEffort, "high")
	}
}

// TestHandleCommand_ModelChangeClearsConversationEffort (spec C5 clearing
// paragraph: "A model change clears the effort stored for that surface: ...
// a non-web model change clears SessionMeta.ReasoningEffort"; BDD §1127:
// the next turn resolves "never a stale 'high'") — the model change is the
// REAL path a CLI conversation takes today: /model <name> →
// rt.SwitchModel → ApplyAgentModel. A FAILED model change is not a model
// change: the stored effort must survive it.
func TestHandleCommand_ModelChangeClearsConversationEffort(t *testing.T) {
	al, agent, store := newEffortAgentLoop(t)

	a, err := store.NewChannelSession("telegram", "tg-instance", "peer-clear", agent.ID, "clear-probe")
	if err != nil {
		t.Fatalf("NewChannelSession: %v", err)
	}
	opts := &processOptions{SessionKey: a.ID}

	// Setup via the command itself: store high.
	if reply := runEffortCommand(t, al, agent, opts, "/effort high"); !strings.Contains(reply, "high") {
		t.Fatalf("/effort high reply=%q", reply)
	}

	// The real non-web model-change path.
	if reply := runEffortCommand(t, al, agent, opts, "/model deepseek-chat"); !strings.Contains(reply, "deepseek-chat") {
		t.Fatalf("/model deepseek-chat reply=%q, want a switch confirmation", reply)
	}
	if agent.Model != "deepseek-chat" {
		t.Fatalf("agent.Model=%q after /model, want deepseek-chat — the switch must have really happened for this test to mean anything", agent.Model)
	}
	got, err := store.GetMeta(a.ID)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.ReasoningEffort != "" {
		t.Fatalf("stored effort after a successful model change = %q, want empty — "+
			"a model change must clear the conversation's stored effort (C5 clearing paragraph)", got.ReasoningEffort)
	}

	// Negative half: a FAILED model change is not a model change. Store
	// again, then attempt an unknown model — the effort must survive.
	if reply := runEffortCommand(t, al, agent, opts, "/effort high"); !strings.Contains(reply, "high") {
		t.Fatalf("/effort high (re-set) reply=%q", reply)
	}
	reply := runEffortCommand(t, al, agent, opts, "/model no-such-model")
	if !strings.Contains(strings.ToLower(reply), "no-such-model") {
		t.Fatalf("/model no-such-model reply=%q, want the model-resolution error", reply)
	}
	if agent.Model != "deepseek-chat" {
		t.Fatalf("agent.Model=%q after failed switch, want deepseek-chat (unchanged)", agent.Model)
	}
	got, err = store.GetMeta(a.ID)
	if err != nil {
		t.Fatalf("GetMeta after failed switch: %v", err)
	}
	if got.ReasoningEffort != "high" {
		t.Fatalf("stored effort after a FAILED model change = %q, want %q — "+
			"a failed switch is not a model change; clearing here would wipe effort on user error", got.ReasoningEffort, "high")
	}
}
