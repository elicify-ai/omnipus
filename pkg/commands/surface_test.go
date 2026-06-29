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
		Name:     "testcmd",
		Surfaces: []Surface{SurfaceCLI, SurfaceChannel},
	}
	if def.AllowsSurface(SurfaceWeb) {
		t.Error("testcmd must NOT allow SurfaceWeb")
	}
	if !def.AllowsSurface(SurfaceCLI) {
		t.Error("testcmd must allow SurfaceCLI")
	}
	if !def.AllowsSurface(SurfaceChannel) {
		t.Error("testcmd must allow SurfaceChannel")
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
		// config is CLI+Channel only
		{"config", []Surface{SurfaceCLI, SurfaceChannel}, "webchat", OutcomePassthrough},
		{"config", []Surface{SurfaceCLI, SurfaceChannel}, "cli", OutcomeHandled},
		{"config", []Surface{SurfaceCLI, SurfaceChannel}, "telegram", OutcomeHandled},
		// clear is all surfaces
		{"clear", []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel}, "webchat", OutcomeHandled},
		// tasks is CLI+Channel only
		{"tasks", []Surface{SurfaceCLI, SurfaceChannel}, "webchat", OutcomePassthrough},
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
			t.Errorf(
				"cmd=%q channel=%q: outcome=%v, want %v",
				tc.cmd,
				tc.channel,
				res.Outcome,
				tc.want,
			)
		}
		if tc.want == OutcomeHandled && !called {
			t.Errorf("cmd=%q channel=%q: handler not called", tc.cmd, tc.channel)
		}
		if tc.want == OutcomePassthrough && called {
			t.Errorf(
				"cmd=%q channel=%q: handler must not be called on passthrough",
				tc.cmd,
				tc.channel,
			)
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

// TestRegistry_AliasResolution verifies alias pairs (TDD #5, DS-2).
// Note: the "skill"/"use" pair is removed (D1 hard removal).
func TestRegistry_AliasResolution(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	pairs := []struct{ canonical, alias string }{
		{"tasks", "subagents"},
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

	// Confirm "skill" and "use" are NOT in the registry (D1 hard removal).
	for _, name := range []string{"skill", "use"} {
		if _, found := reg.Lookup(name); found {
			t.Errorf("Registry.Lookup(%q) must return not-found (D1 hard removal)", name)
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
// Updated for D1 (skill removed) and D7/D9 (agents+skills now on web).
func TestHelpFormatter_SurfaceFilter(t *testing.T) {
	defs := BuiltinDefinitions()

	// Web surface: clear, help, model, cancel, agents, skills = 6 commands.
	webHelp := formatHelpMessage(defs, SurfaceWeb)
	for _, name := range []string{"clear", "help", "model", "cancel", "agents", "skills"} {
		if !containsWord(webHelp, "/"+name) {
			t.Errorf("web help must contain /%s, got:\n%s", name, webHelp)
		}
	}
	// Non-web-only commands must not appear in web help (tasks, channels, status, config).
	for _, name := range []string{"tasks", "channels", "status", "config"} {
		if containsWord(webHelp, "/"+name) {
			t.Errorf("web help must NOT contain /%s (CLI/Channel only), got:\n%s", name, webHelp)
		}
	}
	// Removed and hidden/deprecated commands must not appear as their own command entry.
	// We check "/<name> -" (the help-line format used by formatHelpMessage) to avoid
	// false substring matches (e.g. "/skill" inside "/skills - List installed skills").
	for _, name := range []string{"show", "list", "switch", "check", "start", "subagents", "reload", "use", "skill"} {
		if containsWord(webHelp, "/"+name+" -") {
			t.Errorf("web help must NOT contain /%s as a command entry (removed/hidden), got:\n%s", name, webHelp)
		}
	}

	// CLI surface: should show all 10 canonical commands.
	cliHelp := formatHelpMessage(defs, SurfaceCLI)
	allCanonical := []string{
		"clear",
		"help",
		"model",
		"cancel",
		"agents",
		"tasks",
		"skills",
		"channels",
		"status",
		"config",
	}
	for _, name := range allCanonical {
		if !containsWord(cliHelp, "/"+name) {
			t.Errorf("cli help must contain /%s, got:\n%s", name, cliHelp)
		}
	}
}

// TestChannelRegistrationFilter verifies that only Channel-surfaced, non-Hidden defs
// would pass the filter used in manager.StartAll (TDD #9, US-5/AS-1).
// Updated for D1 (skill removed) and D7/D9 (agents+skills gain SurfaceChannel).
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

	// The channel set must include all 10 canonical commands.
	wantInChannel := []string{
		"clear",
		"help",
		"model",
		"cancel",
		"agents",
		"tasks",
		"skills",
		"channels",
		"status",
		"config",
	}
	names := make(map[string]bool, len(channelDefs))
	for _, d := range channelDefs {
		names[d.Name] = true
	}
	for _, want := range wantInChannel {
		if !names[want] {
			t.Errorf("channel menu should include %q, but it doesn't. menu: %v", want, names)
		}
	}

	// Removed commands must be excluded.
	for _, banned := range []string{"skill", "use"} {
		if names[banned] {
			t.Errorf("removed command %q must not appear in channel registration (D1)", banned)
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
// Updated for D1 (skill removed: 11→10 canonical) and D7/D9 (agents+skills gain SurfaceWeb: 5→6 web).
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

	// 10 canonical (skill removed D1) + 5 hidden/deprecated = 15 total
	if canonical != 10 {
		t.Errorf("expected 10 canonical commands (skill removed D1), got %d", canonical)
	}
	if hidden != 5 {
		t.Errorf(
			"expected 5 hidden/deprecated commands, got %d (start+show+list+switch+check)",
			hidden,
		)
	}

	// Web surface: clear, help, model, cancel, agents, skills = 6 (D7/D9 added agents+skills)
	webCount := 0
	for _, d := range defs {
		if !d.Hidden && d.AllowsSurface(SurfaceWeb) {
			webCount++
		}
	}
	if webCount != 6 {
		t.Errorf("expected 6 web-surface canonical commands (D7/D9: agents+skills added), got %d", webCount)
	}

	// CLI/Channel surface: all 10 canonical
	cliCount := 0
	for _, d := range defs {
		if !d.Hidden && d.AllowsSurface(SurfaceCLI) {
			cliCount++
		}
	}
	if cliCount != 10 {
		t.Errorf("expected 10 CLI-surface canonical commands (skill removed D1), got %d", cliCount)
	}
}

// TestDeliveryFields verifies delivery modes for the canonical commands.
// Updated for D1 (skill removed), D7 (agents=client), D9 (skills=client).
func TestDeliveryFields(t *testing.T) {
	defs := BuiltinDefinitions()
	reg := NewRegistry(defs)

	// Client-delivered commands (web SPA handles them locally).
	clientCmds := []string{"clear", "help", "model", "cancel", "agents", "skills"}
	for _, name := range clientCmds {
		def, ok := reg.Lookup(name)
		if !ok {
			t.Errorf("%q not found", name)
			continue
		}
		if def.EffectiveDelivery() != DeliveryClient {
			t.Errorf("%q: Delivery=%q, want %q", name, def.EffectiveDelivery(), DeliveryClient)
		}
	}

	// Confirm /skill and /use are absent (D1).
	for _, name := range []string{"skill", "use"} {
		if _, found := reg.Lookup(name); found {
			t.Errorf("Registry must not contain %q (D1 hard removal)", name)
		}
	}
}
