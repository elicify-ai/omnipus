// loop_slash_test.go: tests for slash, skill and memory commands

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// --- moved from loop.go tests 2026-09-15 ---

// TestHandleCommand_UseTokenIsNormalMessage verifies that "/use <x>" is no longer a
// skill-activation command (D1): it is delivered as a normal chat message, not handled.
func TestHandleCommand_UseTokenIsNormalMessage(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	agent := al.GetRegistry().GetDefaultAgent()

	// "/use" is no longer a registered command, so it falls through as a normal message.
	opts := processOptions{}
	_, handled := al.handleCommand(context.Background(), bus.InboundMessage{
		Channel: "telegram",
		Sender: bus.SenderInfo{
			CanonicalID: "telegram:123",
		},
		ChatID:  "chat-1",
		Content: "/use missing explain how to list files",
	}, agent, &opts)
	if handled {
		t.Fatal("/use must no longer be handled — it should pass through as a normal message (D1/D4)")
	}
}

// TestApplyExplicitSkillCommand_OneShot verifies the one-shot activation semantics (R1/D2):
// - "/<skill> message" → skill forced, UserMessage = trailing message
// - "/<skill>" alone   → skill forced, UserMessage unchanged (skill body drives LLM)
// Replaces the old /use-token tests (TestApplyExplicitSkillCommand_Arms* + _Inline*).
func TestApplyExplicitSkillCommand_OneShot(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	if err := os.MkdirAll(
		filepath.Join(cfg.Agents.Defaults.Home, "skills", "finance-news"),
		0o755,
	); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(cfg.Agents.Defaults.Home, "skills", "finance-news", "SKILL.md"),
		[]byte("# Finance News\n\nUse web tools for current finance updates.\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	// ADR-072 D5: absence of a grant list denies every skill now, so this
	// test must explicitly grant "finance-news" — it used to resolve under
	// the old "no allowlist = unrestricted" default.
	if agent.ContextBuilder != nil {
		agent.ContextBuilder.WithSkillAllowlist([]string{"finance-news"})
	}

	t.Run("with_message", func(t *testing.T) {
		opts := &processOptions{
			SessionKey:  "agent:main:test",
			UserMessage: "/finance-news dammi le ultime news",
		}
		matched, handled, reply := al.applyExplicitSkillCommand(opts.UserMessage, agent, opts)
		if !matched {
			t.Fatal("expected /<skill> command to match")
		}
		if handled {
			t.Fatal("/<skill> with message must fall through to LLM (not produce a text reply)")
		}
		if reply != "" {
			t.Fatalf("unexpected reply: %q", reply)
		}
		if opts.UserMessage != "dammi le ultime news" {
			t.Fatalf("opts.UserMessage = %q, want %q", opts.UserMessage, "dammi le ultime news")
		}
		if len(opts.ForcedSkills) != 1 || opts.ForcedSkills[0] != "finance-news" {
			t.Fatalf("opts.ForcedSkills = %#v, want [finance-news]", opts.ForcedSkills)
		}
	})

	t.Run("alone_no_message", func(t *testing.T) {
		opts := &processOptions{
			SessionKey:  "agent:main:test",
			UserMessage: "/finance-news",
		}
		matched, handled, reply := al.applyExplicitSkillCommand(opts.UserMessage, agent, opts)
		if !matched {
			t.Fatal("expected /<skill> alone to match")
		}
		if handled {
			t.Fatal("/<skill> alone must fall through so the LLM turn runs (not produce a text reply)")
		}
		if reply != "" {
			t.Fatalf("unexpected reply: %q", reply)
		}
		// UserMessage unchanged — the skill body drives the LLM turn (R1).
		if opts.UserMessage != "/finance-news" {
			t.Fatalf("opts.UserMessage = %q, want unchanged %q", opts.UserMessage, "/finance-news")
		}
		if len(opts.ForcedSkills) != 1 || opts.ForcedSkills[0] != "finance-news" {
			t.Fatalf("opts.ForcedSkills = %#v, want [finance-news]", opts.ForcedSkills)
		}
	})
}

// TestActiveSkillNames_DoesNotUnionGrantList is ADR-072 D1/D3's regression:
// before this fix, activeSkillNames unioned agent.SkillsFilter (the agent's
// ENTIRE per-agent grant list) into every turn's active skills, force-loading
// the whole grant list on every message — the exact mechanism the on-demand
// Skill tool replaces. A grant merely makes a skill LOADABLE (skillAllowed);
// it must not make it ACTIVE.
func TestActiveSkillNames_DoesNotUnionGrantList(t *testing.T) {
	agent := &AgentInstance{
		SkillsFilter: []string{"summarize", "plan", "daily-briefing"},
	}

	t.Run("a grant list with no ForcedSkills yields no active skills", func(t *testing.T) {
		got := activeSkillNames(agent, processOptions{})
		if len(got) != 0 {
			t.Errorf("activeSkillNames with only a grant list (no ForcedSkills) = %v, want empty — "+
				"a grant must not force-load the skill every turn (ADR-072 D1/D3)", got)
		}
	})

	t.Run("ForcedSkills outside the grant list still surfaces (resolution is independent of the grant here)", func(t *testing.T) {
		// activeSkillNames itself does not consult skillAllowed — the grant
		// gate is enforced at the point a name is ADDED to ForcedSkills
		// (applyExplicitSkillCommand's ResolveSkillName call, or delegate's
		// D9 gate). This test pins that activeSkillNames' own output is a
		// pure function of ForcedSkills, not of SkillsFilter, in either
		// direction — SkillsFilter neither adds to nor restricts the result.
		got := activeSkillNames(agent, processOptions{ForcedSkills: []string{"ad-hoc-skill"}})
		if len(got) != 1 || got[0] != "ad-hoc-skill" {
			t.Errorf("activeSkillNames = %v, want [ad-hoc-skill]", got)
		}
	})
}

// TestActiveSkillNames_ForcedSkillsStillHonored is the regression that the
// two legitimate one-shot activation channels — the /<slug> slash command
// (applyExplicitSkillCommand) and delegate's requested_skill (D9,
// spawnSubTurn's ForcedSkills append) — still work after the grant-list union
// was removed: both operate purely through opts.ForcedSkills.
func TestActiveSkillNames_ForcedSkillsStillHonored(t *testing.T) {
	agent := &AgentInstance{} // no grant list at all — must not matter

	got := activeSkillNames(agent, processOptions{
		ForcedSkills: []string{"plan-spec", "PLAN-SPEC", "  summarize  "},
	})

	if len(got) != 2 {
		t.Fatalf("activeSkillNames = %v, want 2 entries (dedup case-insensitively, trim whitespace)", got)
	}
	seen := map[string]bool{}
	for _, name := range got {
		seen[name] = true
	}
	if !seen["plan-spec"] && !seen["PLAN-SPEC"] {
		t.Errorf("activeSkillNames = %v, want a plan-spec entry (deduped from the two case variants)", got)
	}
	if !seen["summarize"] {
		t.Errorf("activeSkillNames = %v, want a trimmed \"summarize\" entry", got)
	}
}

// TestApplyMemoryCommandPrompt_Matrix verifies the rewrite for all three
// commands, with and without trailing args.
func TestApplyMemoryCommandPrompt_Matrix(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	type row struct {
		id          string
		input       string
		wantMatched bool
		wantHandled bool
		wantContain []string // substrings opts.UserMessage must contain after rewrite
	}

	rows := []row{
		{
			id: "remember-with-args", input: "/remember always use pnpm",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"always use pnpm", "'remember' tool"},
		},
		{
			id: "remember-bare", input: "/remember",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"'remember' tool"},
		},
		{
			id: "recall-with-args", input: "/recall what did we decide",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"what did we decide", "recall_memory", "recall_conversation"},
		},
		{
			id: "recall-bare", input: "/recall",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"recall_memory", "recall_conversation"},
		},
		{
			id: "retrospective-bare", input: "/retrospective",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"run_retrospective"},
		},
		{
			id: "retrospective-with-args", input: "/retrospective focus on the outage",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"run_retrospective", "focus on the outage"},
		},
		// Non-memory builtin: must not match this hook at all.
		{id: "help-unaffected", input: "/help", wantMatched: false},
		{id: "clear-unaffected", input: "/clear", wantMatched: false},
		// Unknown slash text: must not match.
		{id: "unknown-unaffected", input: "/nonesuch hello", wantMatched: false},
		// Normal text (no slash): must not match.
		{id: "plain-text-unaffected", input: "hello there", wantMatched: false},
	}

	for _, r := range rows {
		t.Run(r.id, func(t *testing.T) {
			opts := &processOptions{
				SessionKey:  "test-session",
				UserMessage: r.input,
			}
			matched, handled, reply := al.applyMemoryCommandPrompt(r.input, opts)

			if matched != r.wantMatched {
				t.Fatalf("matched=%v, want %v (reply=%q)", matched, r.wantMatched, reply)
			}
			if !matched {
				// Unmatched rows must leave opts.UserMessage untouched.
				if opts.UserMessage != r.input {
					t.Errorf("UserMessage=%q, want unchanged %q", opts.UserMessage, r.input)
				}
				return
			}
			if handled != r.wantHandled {
				t.Fatalf("handled=%v, want %v", handled, r.wantHandled)
			}
			if reply != "" {
				t.Fatalf("reply=%q, want empty (turn continues to the LLM)", reply)
			}
			for _, sub := range r.wantContain {
				if !strings.Contains(opts.UserMessage, sub) {
					t.Errorf("UserMessage=%q, want to contain %q", opts.UserMessage, sub)
				}
			}
			// The rewritten message must differ from the raw slash command —
			// the model must never see the literal "/remember ..." token.
			if opts.UserMessage == r.input {
				t.Errorf("UserMessage unchanged (%q) — expected a steering-prompt rewrite", opts.UserMessage)
			}
		})
	}
}

// TestApplyMemoryCommandPrompt_Precedence pins the precedence between the
// skill hook (applyExplicitSkillCommand) and the memory hook
// (applyMemoryCommandPrompt), the same seam handleCommand wires them into:
// the skill hook runs first and, because remember/recall/retrospective are
// registered builtins (pkg/commands/cmd_memory.go), its own builtin-wins
// check (D3) makes it return matched=false for all three names — so the
// memory hook is what actually performs the rewrite. A skill sharing one of
// these names must not be able to shadow the builtin either.
func TestApplyMemoryCommandPrompt_Precedence(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	// Install a skill whose slug collides with a memory command name.
	writeSkillFile(t, cfg.Agents.Defaults.Home, "remember")

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	input := "/remember buy milk"
	opts := &processOptions{SessionKey: "test-session", UserMessage: input}

	// Step 1: the skill hook must defer (builtin wins, D3) — matched=false.
	skillMatched, _, _ := al.applyExplicitSkillCommand(input, agent, opts)
	if skillMatched {
		t.Fatal("applyExplicitSkillCommand must not match 'remember' — it is a registered builtin (D3 builtin-wins)")
	}
	if len(opts.ForcedSkills) > 0 {
		t.Fatalf("ForcedSkills must be empty when the builtin wins, got %v", opts.ForcedSkills)
	}
	if opts.UserMessage != input {
		t.Fatalf("UserMessage must be untouched by the skill hook, got %q", opts.UserMessage)
	}

	// Step 2: the memory hook runs next (same order as handleCommand) and
	// performs the actual rewrite.
	memMatched, memHandled, _ := al.applyMemoryCommandPrompt(input, opts)
	if !memMatched {
		t.Fatal("applyMemoryCommandPrompt must match '/remember'")
	}
	if memHandled {
		t.Fatal("applyMemoryCommandPrompt must not short-circuit (handled=false) — the turn must reach the LLM")
	}
	if !strings.Contains(opts.UserMessage, "buy milk") {
		t.Fatalf("UserMessage=%q must contain the verbatim args after rewrite", opts.UserMessage)
	}
}

// TestApplyMemoryCommandPrompt_MainAgentDegradesGracefully documents that
// the rewrite hook is agent-agnostic — it operates purely on command text,
// with no agent-identity branch of its own — so it fires and produces a
// steering prompt the same way regardless of which agent resolves the turn.
// (Historical note: this used to matter because agentID "main" was excluded
// from the remember/recall_memory/run_retrospective registration by a
// hardcoded `agentID != "main"` check in instance.go. That gate is gone —
// TestMemoryTools_RegisteredRegardlessOfAgentID in
// recall_conversation_registration_test.go pins its removal directly. This
// test's name survives for historical continuity; what it actually proves
// now is narrower: the prompt-rewrite hook itself never needed an
// agent-identity branch, degrade or otherwise.)
func TestApplyMemoryCommandPrompt_MainAgentDegradesGracefully(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	opts := &processOptions{SessionKey: "test-session", UserMessage: "/retrospective"}
	matched, handled, _ := al.applyMemoryCommandPrompt("/retrospective", opts)
	if !matched || handled {
		t.Fatalf("matched=%v handled=%v, want matched=true handled=false", matched, handled)
	}
	if !strings.Contains(opts.UserMessage, "run_retrospective") {
		t.Fatalf(
			"UserMessage=%q must still contain the steering prompt regardless of which agent handles the turn",
			opts.UserMessage,
		)
	}
}
