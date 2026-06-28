package commands

import (
	"context"
	"strings"
	"testing"
)

func findDefinitionByName(t *testing.T, defs []Definition, name string) Definition {
	t.Helper()
	for _, def := range defs {
		if def.Name == name {
			return def
		}
	}
	t.Fatalf("missing /%s definition", name)
	return Definition{}
}

// TestBuiltinHelpHandler_ReturnsFormattedMessage verifies /help lists the
// canonical noun commands (not the hidden deprecated ones) for the caller's surface.
// Updated from the pre-harmonization test: /show, /list, /use no longer appear;
// /status, /agents, /config etc. appear on non-web surfaces.
func TestBuiltinHelpHandler_ReturnsFormattedMessage(t *testing.T) {
	defs := BuiltinDefinitions()
	helpDef := findDefinitionByName(t, defs, "help")
	if helpDef.Handler == nil {
		t.Fatalf("/help handler should not be nil")
	}

	// Call from CLI surface — should see all 11 canonical commands.
	var reply string
	err := helpDef.Handler(context.Background(), Request{
		Channel: "cli",
		Text:    "/help",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("/help handler error: %v", err)
	}

	// Canonical commands must appear.
	for _, name := range []string{"clear", "help", "model", "skill", "cancel", "agents", "tasks", "skills", "channels", "status", "config"} {
		if !strings.Contains(reply, "/"+name) {
			t.Errorf("/help cli: missing /%s in output:\n%s", name, reply)
		}
	}

	// Hidden/deprecated commands must NOT appear.
	for _, name := range []string{"show", "list", "switch", "check", "start", "use", "subagents", "reload"} {
		// Check that the name doesn't appear as a /name entry (it could appear in descriptions)
		// We check for the usage-format "/<name> " or "/<name>\n" or "/<name> -"
		if strings.Contains(reply, "/"+name+" -") || strings.HasPrefix(reply, "/"+name) {
			t.Errorf("/help cli: deprecated /%s must not appear as a command in output:\n%s", name, reply)
		}
	}
}

// TestBuiltinShowChannel_PreservesUserVisibleBehavior verifies the deprecated
// /show channel sub-command still works on channel surfaces (back-compat).
func TestBuiltinShowChannel_PreservesUserVisibleBehavior(t *testing.T) {
	defs := BuiltinDefinitions()
	ex := NewExecutor(NewRegistry(defs), nil)

	cases := []string{"telegram", "whatsapp"}
	for _, channel := range cases {
		var reply string
		res := ex.Execute(context.Background(), Request{
			Channel: channel,
			Text:    "/show channel",
			Reply: func(text string) error {
				reply = text
				return nil
			},
		})
		if res.Outcome != OutcomeHandled {
			t.Fatalf("/show channel on %s: outcome=%v, want=%v", channel, res.Outcome, OutcomeHandled)
		}
		want := "Current Channel: " + channel
		if reply != want {
			t.Fatalf("/show channel reply=%q, want=%q", reply, want)
		}
	}
}

// TestBuiltinListChannels_UsesGetEnabledChannels verifies the deprecated /list channels
// still works on channel surfaces (back-compat).
func TestBuiltinListChannels_UsesGetEnabledChannels(t *testing.T) {
	rt := &Runtime{
		GetEnabledChannels: func() []string {
			return []string{"telegram", "slack"}
		},
	}
	defs := BuiltinDefinitions()
	ex := NewExecutor(NewRegistry(defs), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram", // deprecated /list has empty surfaces — runs on any channel
		Text:    "/list channels",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/list channels: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !strings.Contains(reply, "telegram") || !strings.Contains(reply, "slack") {
		t.Fatalf("/list channels reply=%q, want telegram and slack", reply)
	}
}

// TestBuiltinShowAgents_RestoresOldBehavior verifies the deprecated /show agents still works.
func TestBuiltinShowAgents_RestoresOldBehavior(t *testing.T) {
	rt := &Runtime{
		ListAgentIDs: func() []string {
			return []string{"default", "coder"}
		},
	}
	defs := BuiltinDefinitions()
	ex := NewExecutor(NewRegistry(defs), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/show agents",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/show agents: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !strings.Contains(reply, "default") || !strings.Contains(reply, "coder") {
		t.Fatalf("/show agents reply=%q, want agent IDs", reply)
	}
}

// TestBuiltinListAgents_RestoresOldBehavior verifies the deprecated /list agents still works.
func TestBuiltinListAgents_RestoresOldBehavior(t *testing.T) {
	rt := &Runtime{
		ListAgentIDs: func() []string {
			return []string{"default", "coder"}
		},
	}
	defs := BuiltinDefinitions()
	ex := NewExecutor(NewRegistry(defs), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/list agents",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/list agents: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !strings.Contains(reply, "default") || !strings.Contains(reply, "coder") {
		t.Fatalf("/list agents reply=%q, want agent IDs", reply)
	}
}

// TestBuiltinListSkills_UsesRuntimeSkillNames verifies the deprecated /list skills still works.
func TestBuiltinListSkills_UsesRuntimeSkillNames(t *testing.T) {
	rt := &Runtime{
		ListSkillNames: func() []string {
			return []string{"shell", "git"}
		},
	}
	defs := BuiltinDefinitions()
	ex := NewExecutor(NewRegistry(defs), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/list skills",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/list skills: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !strings.Contains(reply, "shell") || !strings.Contains(reply, "git") {
		t.Fatalf("/list skills reply=%q, want installed skill names", reply)
	}
}

// TestBuiltinSkillCommand_PassthroughsToAgentLogic verifies /skill (renamed from /use)
// passes through to agent logic since it has no backend handler.
// The canonical command name is now "skill"; the old alias "use" resolves to the same def.
func TestBuiltinSkillCommand_PassthroughsToAgentLogic(t *testing.T) {
	defs := BuiltinDefinitions()
	ex := NewExecutor(NewRegistry(defs), nil)

	// Test via canonical name /skill
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/skill shell run ls",
	})
	if res.Outcome != OutcomePassthrough {
		t.Fatalf("/skill outcome=%v, want=%v", res.Outcome, OutcomePassthrough)
	}
	if res.Command != "skill" {
		t.Fatalf("/skill command=%q, want=%q", res.Command, "skill")
	}

	// Test via back-compat alias /use — resolves to "skill" canonical.
	resUse := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/use shell run ls",
	})
	if resUse.Outcome != OutcomePassthrough {
		t.Fatalf("/use outcome=%v, want=%v", resUse.Outcome, OutcomePassthrough)
	}
	if resUse.Command != "skill" {
		t.Fatalf("/use command=%q, want=%q (canonical)", resUse.Command, "skill")
	}
}

// TestBuiltinUseCommand_PassthroughsToAgentLogic is kept for legacy CI compatibility.
// The old test expected Command=="use" but the canonical is now "skill".
// This asserts the current correct behavior.
func TestBuiltinUseCommand_PassthroughsToAgentLogic(t *testing.T) {
	defs := BuiltinDefinitions()
	ex := NewExecutor(NewRegistry(defs), nil)

	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/use shell run ls",
	})
	if res.Outcome != OutcomePassthrough {
		t.Fatalf("/use outcome=%v, want=%v", res.Outcome, OutcomePassthrough)
	}
	// The resolved command name is the canonical "skill", not the alias "use".
	if res.Command != "skill" {
		t.Fatalf("/use command=%q, want=%q (canonical name after rename)", res.Command, "skill")
	}
}
