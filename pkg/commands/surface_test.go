package commands

import (
	"context"
	"testing"
)

// TestSurfaceForChannel verifies the channel→surface mapping (TDD #1).
func TestSurfaceForChannel(t *testing.T) {
	cases := []struct {
		channel string
		want    Surface
	}{
		{"webchat", SurfaceWeb},
		{"cli", SurfaceCLI},
		{"telegram", SurfaceChannel},
		{"discord", SurfaceChannel},
		{"slack", SurfaceChannel},
		{"whatsapp", SurfaceChannel},
		{"", SurfaceChannel}, // empty string → channel (safe default)
	}
	for _, tc := range cases {
		got := SurfaceForChannel(tc.channel)
		if got != tc.want {
			t.Errorf("SurfaceForChannel(%q) = %v, want %v", tc.channel, got, tc.want)
		}
	}
}

// TestAllowsSurface_EmptySurfaces verifies that empty Surfaces = all surfaces (TDD #2, FR-001).
func TestAllowsSurface_EmptySurfaces(t *testing.T) {
	def := Definition{Name: "legacy", Surfaces: nil}
	for _, s := range []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel} {
		if !def.AllowsSurface(s) {
			t.Errorf("empty Surfaces must allow all surfaces, failed for %v", s)
		}
	}
}

// TestAllowsSurface_Membership verifies membership check.
func TestAllowsSurface_Membership(t *testing.T) {
	def := Definition{
		Name:     "agents",
		Surfaces: []Surface{SurfaceCLI, SurfaceChannel},
	}
	if def.AllowsSurface(SurfaceWeb) {
		t.Error("agents must NOT allow SurfaceWeb")
	}
	if !def.AllowsSurface(SurfaceCLI) {
		t.Error("agents must allow SurfaceCLI")
	}
	if !def.AllowsSurface(SurfaceChannel) {
		t.Error("agents must allow SurfaceChannel")
	}
}

// TestExecutor_SurfaceGating is the core table-driven gating test (TDD #3, DS-1).
// It verifies FR-002: non-web commands from webchat → Passthrough; from CLI → Handled.
func TestExecutor_SurfaceGating(t *testing.T) {
	// Build a dummy handler that records execution.
	type row struct {
		cmd      string
		surfaces []Surface
		channel  string
		want     Outcome
	}
	rows := []row{
		// agents is CLI+Channel only
		{"agents", []Surface{SurfaceCLI, SurfaceChannel}, "webchat", OutcomePassthrough},
		{"agents", []Surface{SurfaceCLI, SurfaceChannel}, "cli", OutcomeHandled},
		{"agents", []Surface{SurfaceCLI, SurfaceChannel}, "telegram", OutcomeHandled},
		// clear is all surfaces
		{"clear", []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel}, "webchat", OutcomeHandled},
		// config is CLI+Channel only
		{"config", []Surface{SurfaceCLI, SurfaceChannel}, "webchat", OutcomePassthrough},
		// empty surfaces = all
		{"legacy", nil, "webchat", OutcomeHandled},
	}

	for _, tc := range rows {
		called := false
		def := Definition{
			Name:     tc.cmd,
			Surfaces: tc.surfaces,
			Handler: func(_ context.Context, _ Request, _ *Runtime) error {
				called = true
				return nil
			},
		}
		ex := NewExecutor(NewRegistry([]Definition{def}), nil)
		res := ex.Execute(context.Background(), Request{
			Channel: tc.channel,
			Text:    "/" + tc.cmd,
			Reply:   func(string) error { return nil },
		})
		if res.Outcome != tc.want {
			t.Errorf("cmd=%q channel=%q: outcome=%v, want %v", tc.cmd, tc.channel, res.Outcome, tc.want)
		}
		if tc.want == OutcomeHandled && !called {
			t.Errorf("cmd=%q channel=%q: handler not called", tc.cmd, tc.channel)
		}
		if tc.want == OutcomePassthrough && called {
			t.Errorf("cmd=%q channel=%q: handler must not be called on passthrough", tc.cmd, tc.channel)
		}
	}
}

// TestExecutor_NormalTextPassthrough verifies normal text (not a command) still passes through (TDD #4).
func TestExecutor_NormalTextPassthrough(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), nil)
	res := ex.Execute(context.Background(), Request{
		Channel: "webchat",
		Text:    "hello there",
	})
	if res.Outcome != OutcomePassthrough {
		t.Errorf("normal text: outcome=%v, want=%v", res.Outcome, OutcomePassthrough)
	}
}

// TestExecutor_WebAliasOfNonWebCommandPassesThrough verifies edge case:
// /subagents is an alias of /tasks (non-web) → webchat → Passthrough.
func TestExecutor_WebAliasOfNonWebCommandPassesThrough(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), nil)
	res := ex.Execute(context.Background(), Request{
		Channel: "webchat",
		Text:    "/subagents",
		Reply:   func(string) error { return nil },
	})
	if res.Outcome != OutcomePassthrough {
		t.Errorf("/subagents from webchat: outcome=%v, want Passthrough", res.Outcome)
	}
}

// TestRegistry_AliasResolution verifies all rename pairs (TDD #5, DS-2, US-1/AS-2).
func TestRegistry_AliasResolution(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	pairs := []struct{ canonical, alias string }{
		{"tasks", "subagents"},
		{"skill", "use"},
		{"config", "reload"},
	}
	for _, p := range pairs {
		canonDef, okCanon := reg.Lookup(p.canonical)
		aliasDef, okAlias := reg.Lookup(p.alias)

		if !okCanon {
			t.Errorf("canonical %q not found in registry", p.canonical)
			continue
		}
		if !okAlias {
			t.Errorf("alias %q not found in registry", p.alias)
			continue
		}
		if canonDef.Name != aliasDef.Name {
			t.Errorf("%q→%q: canonical.Name=%q, alias.Name=%q (should be same definition)",
				p.alias, p.canonical, canonDef.Name, aliasDef.Name)
		}
		// The canonical name must match the expected canonical.
		if canonDef.Name != p.canonical {
			t.Errorf("expected canonical name %q, got %q", p.canonical, canonDef.Name)
		}
	}
}

// TestCancelHasNoAliases is maintained alongside cmd_cancel_test.go (TDD #6, US-1/AS-4).
// This test verifies the /cancel command in BuiltinDefinitions has no aliases.
// It is kept here as an additional layer; cmd_cancel_test.go remains unchanged.
func TestCancelHasNoAliasesInBuiltins(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	def, ok := reg.Lookup("cancel")
	if !ok {
		t.Fatal("/cancel not in registry")
	}
	if len(def.Aliases) != 0 {
		t.Errorf("/cancel must have no aliases, got: %v", def.Aliases)
	}
}

// TestDefToSlashCommandMapper covers TDD #7 (US-3/AS-4,5).
// The mapper is in the gateway package; here we test the surface/definition logic
// that feeds it. The full mapper is tested in pkg/gateway/rest_commands_test.go.

// TestHelpFormatter_SurfaceFilter tests the surface filtering and hidden exclusion (TDD #8, US-5/AS-2).
func TestHelpFormatter_SurfaceFilter(t *testing.T) {
	defs := BuiltinDefinitions()

	// Web surface: should show only the 5 web-capable commands.
	webHelp := formatHelpMessage(defs, SurfaceWeb)
	for _, name := range []string{"clear", "help", "model", "skill", "cancel"} {
		if !containsWord(webHelp, "/"+name) {
			t.Errorf("web help must contain /%s, got:\n%s", name, webHelp)
		}
	}
	// Non-web commands must not appear in web help.
	for _, name := range []string{"agents", "tasks", "skills", "channels", "status", "config"} {
		if containsWord(webHelp, "/"+name) {
			t.Errorf("web help must NOT contain /%s, got:\n%s", name, webHelp)
		}
	}
	// Hidden/deprecated commands must not appear at all.
	for _, name := range []string{"show", "list", "switch", "check", "start", "subagents", "reload", "use"} {
		if containsWord(webHelp, "/"+name) {
			t.Errorf("web help must NOT contain hidden /%s, got:\n%s", name, webHelp)
		}
	}

	// CLI surface: should show all 11 canonical commands.
	cliHelp := formatHelpMessage(defs, SurfaceCLI)
	allCanonical := []string{"clear", "help", "model", "skill", "cancel", "agents", "tasks", "skills", "channels", "status", "config"}
	for _, name := range allCanonical {
		if !containsWord(cliHelp, "/"+name) {
			t.Errorf("cli help must contain /%s, got:\n%s", name, cliHelp)
		}
	}
}

// TestChannelRegistrationFilter verifies that only Channel-surfaced, non-Hidden defs
// would pass the filter used in manager.StartAll (TDD #9, US-5/AS-1).
func TestChannelRegistrationFilter(t *testing.T) {
	allDefs := BuiltinDefinitions()
	channelDefs := make([]Definition, 0, len(allDefs))
	for _, def := range allDefs {
		if def.Hidden {
			continue
		}
		if !def.AllowsSurface(SurfaceChannel) {
			continue
		}
		channelDefs = append(channelDefs, def)
	}

	// The channel set must include all 11 canonical non-web-only commands.
	// Note: web-only commands are excluded; all-surface commands (clear/help/model/skill/cancel)
	// are included because they also include SurfaceChannel.
	wantInChannel := []string{"clear", "help", "model", "skill", "cancel", "agents", "tasks", "skills", "channels", "status", "config"}
	names := make(map[string]bool, len(channelDefs))
	for _, d := range channelDefs {
		names[d.Name] = true
	}
	for _, want := range wantInChannel {
		if !names[want] {
			t.Errorf("channel menu should include %q, but it doesn't. menu: %v", want, names)
		}
	}

	// Hidden commands must be excluded.
	for _, d := range channelDefs {
		if d.Hidden {
			t.Errorf("hidden command %q must not appear in channel registration", d.Name)
		}
	}
}

// containsWord is a simple substring check for test assertions.
func containsWord(text, word string) bool {
	return len(text) >= len(word) && containsSubstring(text, word)
}

func containsSubstring(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestBuiltinDefinitions_CountsAndSurfaces verifies the complete set.
func TestBuiltinDefinitions_CountsAndSurfaces(t *testing.T) {
	defs := BuiltinDefinitions()

	canonical := 0
	hidden := 0
	for _, d := range defs {
		if d.Hidden {
			hidden++
		} else {
			canonical++
		}
	}

	// 11 canonical + 5 hidden/deprecated = 16 total
	if canonical != 11 {
		t.Errorf("expected 11 canonical commands, got %d", canonical)
	}
	if hidden != 5 {
		t.Errorf("expected 5 hidden/deprecated commands, got %d (start+show+list+switch+check)", hidden)
	}

	// Web surface: 5 commands
	webCount := 0
	for _, d := range defs {
		if !d.Hidden && d.AllowsSurface(SurfaceWeb) {
			webCount++
		}
	}
	if webCount != 5 {
		t.Errorf("expected 5 web-surface canonical commands, got %d", webCount)
	}

	// CLI/Channel surface: all 11 canonical
	cliCount := 0
	for _, d := range defs {
		if !d.Hidden && d.AllowsSurface(SurfaceCLI) {
			cliCount++
		}
	}
	if cliCount != 11 {
		t.Errorf("expected 11 CLI-surface canonical commands, got %d", cliCount)
	}
}

// TestDeliveryFields verifies FR-007: client delivery for clear/help/model/cancel;
// agent delivery for skill.
func TestDeliveryFields(t *testing.T) {
	defs := BuiltinDefinitions()
	reg := NewRegistry(defs)

	clientCmds := []string{"clear", "help", "model", "cancel"}
	for _, name := range clientCmds {
		def, ok := reg.Lookup(name)
		if !ok {
			t.Errorf("%q not found", name)
			continue
		}
		if def.Delivery != DeliveryClient {
			t.Errorf("%q: Delivery=%q, want %q", name, def.Delivery, DeliveryClient)
		}
	}

	agentCmds := []string{"skill"}
	for _, name := range agentCmds {
		def, ok := reg.Lookup(name)
		if !ok {
			t.Errorf("%q not found", name)
			continue
		}
		if def.Delivery != DeliveryAgent {
			t.Errorf("%q: Delivery=%q, want %q", name, def.Delivery, DeliveryAgent)
		}
	}
}
