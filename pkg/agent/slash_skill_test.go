package agent

// TestResolveSlash_Matrix is the full Dataset-A resolution matrix from the
// unified slash-command + skill menu spec (SC-001). It covers every row in
// Dataset A (A1–A12): force-skill, builtin-wins, unknown→normal-message,
// prefix→normal-message, case-insensitive, /use→message, /skill→message,
// lone /→message, no-skills→message.
//
// Traces to: FR-001/FR-003/FR-004, US1.1–1.5, US2.2, SC-001.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// writeSkillFile creates a minimal SKILL.md under workspace/skills/<name>/.
func writeSkillFile(t *testing.T, workspace, name string) {
	t.Helper()
	dir := filepath.Join(workspace, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "SKILL.md"),
		[]byte("# "+name+"\n\nSkill description for "+name+"."),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(SKILL.md for %s): %v", name, err)
	}
}

// TestResolveSlash_Matrix covers Dataset A from the spec.
func TestResolveSlash_Matrix(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	// Install skills: web-research, web, summarize.
	for _, name := range []string{"web-research", "web", "summarize"} {
		writeSkillFile(t, cfg.Agents.Defaults.Home, name)
	}

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	type row struct {
		id          string
		input       string
		wantMatched bool
		wantHandled bool
		wantSkill   string // canonical name forced, or ""
		wantMsgPart string // non-empty: opts.UserMessage must contain this
	}

	rows := []row{
		// A1: /<skill> <msg> → force-skill, message extracted
		{
			id: "A1", input: "/web-research find X",
			wantMatched: true, wantHandled: false,
			wantSkill: "web-research", wantMsgPart: "find X",
		},
		// A3: /web <msg> → force-skill (exact slug "web")
		{
			id: "A3", input: "/web do Y",
			wantMatched: true, wantHandled: false,
			wantSkill: "web", wantMsgPart: "do Y",
		},
		// A4: /help → builtin wins, not matched by skill parser
		{id: "A4", input: "/help", wantMatched: false},
		// A4b: /clear → builtin wins
		{id: "A4b", input: "/clear", wantMatched: false},
		// A4c: /skills → builtin wins
		{id: "A4c", input: "/skills", wantMatched: false},
		// A4d: /agents → builtin wins
		{id: "A4d", input: "/agents", wantMatched: false},
		// A4e: /cancel → builtin wins
		{id: "A4e", input: "/cancel", wantMatched: false},
		// A6: /nonesuch hi → normal message (unknown, not matched)
		{id: "A6", input: "/nonesuch hi", wantMatched: false},
		// A7: /sum → prefix of "summarize", exact-slug only → normal message
		{id: "A7", input: "/sum", wantMatched: false},
		// A8: /WEB-RESEARCH go → case-insensitive → force-skill
		{
			id: "A8", input: "/WEB-RESEARCH go",
			wantMatched: true, wantHandled: false,
			wantSkill: "web-research", wantMsgPart: "go",
		},
		// A9: /skill web-research go → "/skill" is unknown (D1 removed it) → normal message
		{id: "A9", input: "/skill web-research go", wantMatched: false},
		// A10: /use web-research → "/use" removed (D1) → normal message
		{id: "A10", input: "/use web-research", wantMatched: false},
		// A11: "/" alone → no command name → not matched
		{id: "A11", input: "/", wantMatched: false},
	}

	for _, r := range rows {
		t.Run(r.id, func(t *testing.T) {
			opts := &processOptions{
				SessionKey:  "test-session",
				UserMessage: r.input,
			}
			matched, handled, reply := al.applyExplicitSkillCommand(r.input, agent, opts)

			if matched != r.wantMatched {
				t.Fatalf("matched=%v, want %v (reply=%q)", matched, r.wantMatched, reply)
			}
			if !matched {
				return // no further assertions for non-matched inputs
			}
			if handled != r.wantHandled {
				t.Fatalf("handled=%v, want %v", handled, r.wantHandled)
			}
			if r.wantSkill != "" {
				found := false
				for _, s := range opts.ForcedSkills {
					if s == r.wantSkill {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("ForcedSkills=%v, want %q", opts.ForcedSkills, r.wantSkill)
				}
			}
			if r.wantMsgPart != "" && !strings.Contains(opts.UserMessage, r.wantMsgPart) {
				t.Fatalf("UserMessage=%q, want to contain %q", opts.UserMessage, r.wantMsgPart)
			}
		})
	}
}

// TestResolveSlash_SkillCannotShadowBuiltin verifies that a skill whose slug
// matches a built-in command name cannot shadow the built-in (D3/SC-003).
//
// Traces to: FR-003, US1.3, SC-003.
func TestResolveSlash_SkillCannotShadowBuiltin(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	// Install a skill whose slug is "clear" — same as the /clear built-in.
	writeSkillFile(t, cfg.Agents.Defaults.Home, "clear")

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	opts := &processOptions{SessionKey: "test-session"}
	matched, _, _ := al.applyExplicitSkillCommand("/clear", agent, opts)

	// The skill parser must NOT match — the built-in wins; handleCommand lets
	// the normal executor handle /clear.
	if matched {
		t.Fatal("skill named 'clear' must not shadow the /clear built-in (D3)")
	}
	if len(opts.ForcedSkills) > 0 {
		t.Fatalf("ForcedSkills must be empty when builtin wins, got %v", opts.ForcedSkills)
	}
}

// TestResolveSlash_Matrix_A2 verifies that "/<skill>" alone (no message) is a
// one-shot activation: matched=true, handled=false, skill forced, UserMessage
// unchanged (the original token stays; skill body drives the LLM turn per R1).
//
// Traces to: FR-001 (R1 loop note), US1, SC-001 row A2.
func TestResolveSlash_Matrix_A2(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	writeSkillFile(t, cfg.Agents.Defaults.Home, "web-research")

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	originalMsg := "/web-research"
	opts := &processOptions{
		SessionKey:  "test-session",
		UserMessage: originalMsg,
	}
	matched, handled, reply := al.applyExplicitSkillCommand(originalMsg, agent, opts)

	if !matched {
		t.Fatal("/<skill> alone must match (R1)")
	}
	if handled {
		t.Fatal("/<skill> alone must NOT be handled inline — the LLM turn must run (R1)")
	}
	if reply != "" {
		t.Fatalf("unexpected reply %q — no arm reply in one-shot mode (R1)", reply)
	}
	found := false
	for _, s := range opts.ForcedSkills {
		if s == "web-research" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ForcedSkills=%v, want [web-research]", opts.ForcedSkills)
	}
	// UserMessage unchanged (no trailing message in the input).
	if opts.UserMessage != originalMsg {
		t.Fatalf("UserMessage=%q, want %q (unchanged when no trailing message)", opts.UserMessage, originalMsg)
	}
}

// TestResolveSlash_A12_NoSkillsInstalled verifies that "/<name>" when no skills
// are installed returns matched=false (D4 — passes through as normal message).
//
// Traces to: FR-004, SC-001 row A12.
func TestResolveSlash_A12_NoSkillsInstalled(t *testing.T) {
	// No skills written to workspace.
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
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	opts := &processOptions{SessionKey: "test"}
	matched, _, _ := al.applyExplicitSkillCommand("/web-research", agent, opts)
	if matched {
		t.Fatal("no skills installed: /<name> must not match (A12)")
	}
}

// TestCommands_NoSkillOrUse verifies that neither "skill" nor "use" appears in
// the built-in command definitions on any surface (D1/SC-002/US2.1).
//
// Traces to: FR-002, US2.1, SC-002.
func TestCommands_NoSkillOrUse(t *testing.T) {
	defs := commands.BuiltinDefinitions()
	for _, def := range defs {
		if def.Name == "skill" || def.Name == "use" {
			t.Errorf("BuiltinDefinitions must not contain %q command (D1 hard removal)", def.Name)
		}
		for _, alias := range def.Aliases {
			if alias == "skill" || alias == "use" {
				t.Errorf("command %q has forbidden alias %q (D1 hard removal)", def.Name, alias)
			}
		}
	}
	// Also confirm via the registry Lookup.
	reg := commands.NewRegistry(defs)
	for _, name := range []string{"skill", "use"} {
		if _, found := reg.Lookup(name); found {
			t.Errorf("Registry.Lookup(%q) must return not-found (D1 hard removal)", name)
		}
	}
}

// TestCmdSkills_WebSurface_AndReplyText verifies that /skills has SurfaceWeb
// (D9/FR-008) and that the reply text uses "/<skillname>" not "/skill <skill>".
//
// Traces to: FR-008, FR-012, US6.3, US8.2, SC-001.
func TestCmdSkills_WebSurface_AndReplyText(t *testing.T) {
	defs := commands.BuiltinDefinitions()

	var skillsDef *commands.Definition
	for i := range defs {
		if defs[i].Name == "skills" {
			skillsDef = &defs[i]
			break
		}
	}
	if skillsDef == nil {
		t.Fatal("/skills command not found in BuiltinDefinitions")
	}

	// Must appear on web surface.
	if !skillsDef.AllowsSurface(commands.SurfaceWeb) {
		t.Error("/skills must be on SurfaceWeb (D9)")
	}

	// Handler must use the new "/<skillname>" wording, not "/skill <skill>".
	rt := &commands.Runtime{
		ListSkillNames: func() []string {
			return []string{"web-research", "summarize"}
		},
	}
	reg := commands.NewRegistry(defs)
	ex := commands.NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), commands.Request{
		Channel: "telegram",
		Text:    "/skills",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != commands.OutcomeHandled {
		t.Fatalf("/skills outcome=%v, want Handled", res.Outcome)
	}
	if strings.Contains(reply, "/skill ") {
		t.Errorf("/skills reply must not mention '/skill <skill>' (removed D1); got:\n%s", reply)
	}
	if !strings.Contains(reply, "/<skillname>") {
		t.Errorf("/skills reply must mention '/<skillname>'; got:\n%s", reply)
	}
}

// TestCmdAgents_WebSurface_ClientDelivery verifies that /agents is on SurfaceWeb
// with DeliveryClient so the SPA opens the in-header agent selector (D7/FR-007).
//
// Traces to: FR-007, US5.3.
func TestCmdAgents_WebSurface_ClientDelivery(t *testing.T) {
	defs := commands.BuiltinDefinitions()

	var agentsDef *commands.Definition
	for i := range defs {
		if defs[i].Name == "agents" {
			agentsDef = &defs[i]
			break
		}
	}
	if agentsDef == nil {
		t.Fatal("/agents command not found in BuiltinDefinitions")
	}

	if !agentsDef.AllowsSurface(commands.SurfaceWeb) {
		t.Error("/agents must be on SurfaceWeb (D7)")
	}
	if agentsDef.EffectiveDelivery() != commands.DeliveryClient {
		t.Errorf("/agents delivery=%q, want %q (D7)", agentsDef.EffectiveDelivery(), commands.DeliveryClient)
	}

	// CLI/Channel: the handler must still work (text list).
	rt := &commands.Runtime{
		ListAgentIDs: func() []string {
			return []string{"mia", "jim"}
		},
	}
	reg := commands.NewRegistry(defs)
	ex := commands.NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), commands.Request{
		Channel: "telegram",
		Text:    "/agents",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != commands.OutcomeHandled {
		t.Fatalf("/agents on telegram: outcome=%v, want Handled", res.Outcome)
	}
	if !strings.Contains(reply, "mia") || !strings.Contains(reply, "jim") {
		t.Errorf("/agents reply must list agent IDs on CLI/Channel; got:\n%s", reply)
	}
}

// TestResolveSlash_HiddenBuiltinWins verifies that a skill whose slug matches a
// HIDDEN (deprecated) built-in command name cannot shadow it (D3/F4). Hidden
// back-compat commands like "list" are registered in the registry and must
// therefore win over a same-named skill even though they are excluded from /help
// and GET /commands output.
//
// Traces to: FR-003, D3, F4 (7-reviewer finding).
func TestResolveSlash_HiddenBuiltinWins(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	// Install a skill whose slug is "list" — same as the hidden back-compat built-in.
	writeSkillFile(t, cfg.Agents.Defaults.Home, "list")

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	opts := &processOptions{SessionKey: "test-session"}
	matched, _, _ := al.applyExplicitSkillCommand("/list skills", agent, opts)

	// The skill parser must NOT match — registration-based gate includes hidden
	// commands (D3 "built-ins win", F4 fix).
	if matched {
		t.Fatal("a skill named 'list' must not shadow the hidden /list built-in (D3/F4)")
	}
	if len(opts.ForcedSkills) > 0 {
		t.Fatalf("ForcedSkills must be empty when hidden builtin wins, got %v", opts.ForcedSkills)
	}
}

// TestHelp_CommandsOnly verifies that /help lists commands and does not list skills,
// and that /skill and /use are absent (D10/FR-009/US7).
//
// Traces to: FR-009, US7.1, US7.2.
func TestHelp_CommandsOnly(t *testing.T) {
	defs := commands.BuiltinDefinitions()

	for _, surface := range []struct {
		name    string
		channel string
	}{
		{name: "web", channel: "webchat"},
		{name: "cli", channel: "cli"},
		{name: "channel", channel: "telegram"},
	} {
		t.Run(surface.name, func(t *testing.T) {
			var helpDef *commands.Definition
			for i := range defs {
				if defs[i].Name == "help" {
					helpDef = &defs[i]
					break
				}
			}
			if helpDef == nil {
				t.Fatal("/help not found")
			}

			rt := &commands.Runtime{
				ListDefinitions: func() []commands.Definition {
					return defs
				},
			}
			reg := commands.NewRegistry(defs)
			ex := commands.NewExecutor(reg, rt)

			var reply string
			res := ex.Execute(context.Background(), commands.Request{
				Channel: surface.channel,
				Text:    "/help",
				Reply: func(text string) error {
					reply = text
					return nil
				},
			})
			if res.Outcome != commands.OutcomeHandled {
				t.Fatalf("/help outcome=%v, want Handled", res.Outcome)
			}

			// /skill and /use must not appear.
			for _, banned := range []string{"/skill ", "/skill\n", "/use ", "/use\n"} {
				if strings.Contains(reply, banned) {
					t.Errorf(
						"/help must not mention %q (D1 hard removal); surface=%s, reply:\n%s",
						banned, surface.name, reply,
					)
				}
			}
		})
	}
}
