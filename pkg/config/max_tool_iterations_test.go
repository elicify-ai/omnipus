// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Expected values below are taken from the #904 spec datasets
// (docs/internal/specs/tool-iteration-limit-spec.md, "Test Datasets"), not
// from the implementation.

// maxToolIterConfigJSON builds a loadable config.json whose agents.defaults
// carries extraDefaults (a JSON object fragment body, may be empty).
func maxToolIterConfigJSON(extraDefaults string) string {
	if extraDefaults != "" {
		extraDefaults = ", " + extraDefaults
	}
	return `{
  "version": ` + strconv.Itoa(CurrentVersion) + `,
  "agents": { "defaults": { "default_model": { "provider": "openrouter", "model": "z-ai/glm-5.2" }` + extraDefaults + ` } },
  "providers": [
    { "provider": "openrouter", "model": "z-ai/glm-5.2", "api_base": "https://openrouter.ai/api/v1", "api_key_ref": "OPENROUTER_API_KEY" }
  ]
}`
}

func writeMaxToolIterConfig(t *testing.T, extraDefaults string) (string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	body := []byte(maxToolIterConfigJSON(extraDefaults))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path, body
}

// TestMaxToolIterationsResolver_Dataset covers Dataset "Resolver" rows 1-11.
func TestMaxToolIterationsResolver_Dataset(t *testing.T) {
	cases := []struct {
		name         string
		global       int
		own          int // 0 = none
		wantEff      int
		wantSource   MaxToolIterationsSource
		wantOverride int
		wantHas      bool
		wantIgnored  bool
	}{
		{"1 default", 200, 0, 200, MaxToolIterationsSourceGlobal, 0, false, false},
		{"2 below", 200, 50, 50, MaxToolIterationsSourceAgent, 50, true, false},
		{"3 equal", 200, 200, 200, MaxToolIterationsSourceAgent, 200, true, false},
		{"4 just above", 200, 201, 200, MaxToolIterationsSourceGlobal, 201, true, true},
		{"5 above", 200, 500, 200, MaxToolIterationsSourceGlobal, 500, true, true},
		{"6 raised", 600, 500, 500, MaxToolIterationsSourceAgent, 500, true, false},
		{"7 min", 1, 0, 1, MaxToolIterationsSourceGlobal, 0, false, false},
		{"8 max", 1000, 1000, 1000, MaxToolIterationsSourceAgent, 1000, true, false},
		{"9 above max hand-edited", 200, 5000, 200, MaxToolIterationsSourceGlobal, 5000, true, true},
		{"10 zero stored", 200, 0, 200, MaxToolIterationsSourceGlobal, 0, false, false},
		{"11 negative stored", 200, -5, 200, MaxToolIterationsSourceGlobal, 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &AgentDefaults{MaxToolIterations: tc.global}
			got := ResolveMaxToolIterations(d, &AgentConfig{ID: "a", MaxToolIterations: tc.own})
			if got.Effective != tc.wantEff || got.Source != tc.wantSource ||
				got.HasOverride != tc.wantHas || got.OverrideIgnored != tc.wantIgnored {
				t.Fatalf("got eff=%d source=%q has=%v ignored=%v; want eff=%d source=%q has=%v ignored=%v",
					got.Effective, got.Source, got.HasOverride, got.OverrideIgnored,
					tc.wantEff, tc.wantSource, tc.wantHas, tc.wantIgnored)
			}
			if tc.wantHas && got.Override != tc.wantOverride {
				t.Fatalf("override = %d, want %d", got.Override, tc.wantOverride)
			}
		})
	}

	// A nil agent is "no own value".
	if got := ResolveMaxToolIterations(&AgentDefaults{MaxToolIterations: 350}, nil); got.Effective != 350 ||
		got.Source != MaxToolIterationsSourceGlobal || got.HasOverride {
		t.Fatalf("nil agent: got %+v, want 350/global/no override", got)
	}
}

// TestMaxToolIterationsGlobal_SavedStates covers Dataset "Saved global"
// through the real loader: effective value, saved-state, raw value, and the
// file bytes left unchanged (D13, D17).
func TestMaxToolIterationsGlobal_SavedStates(t *testing.T) {
	t.Setenv(MaxToolIterationsEnvVar, "")
	cases := []struct {
		name      string
		fragment  string
		wantValue int
		wantState MaxToolIterationsSavedState
		wantRaw   int
		wantHas   bool
	}{
		{"1 valid", `"max_tool_iterations": 200`, 200, MaxToolIterationsSavedOK, 0, false},
		{"2 missing", ``, 200, MaxToolIterationsSavedMissing, 0, false},
		{"3 zero", `"max_tool_iterations": 0`, 200, MaxToolIterationsSavedBelowMin, 0, true},
		{"4 negative", `"max_tool_iterations": -4`, 200, MaxToolIterationsSavedBelowMin, -4, true},
		{"5 max", `"max_tool_iterations": 1000`, 1000, MaxToolIterationsSavedOK, 0, false},
		{"6 just above", `"max_tool_iterations": 1001`, 1000, MaxToolIterationsSavedAboveMax, 1001, true},
		{"7 above", `"max_tool_iterations": 5000`, 1000, MaxToolIterationsSavedAboveMax, 5000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, before := writeMaxToolIterConfig(t, tc.fragment)
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig must never refuse over the limit value: %v", err)
			}
			g := cfg.Agents.Defaults.EffectiveGlobalMaxToolIterations()
			if g.Value != tc.wantValue || g.SavedState != tc.wantState || g.HasSavedRaw != tc.wantHas {
				t.Fatalf("got %+v; want value=%d state=%q hasRaw=%v", g, tc.wantValue, tc.wantState, tc.wantHas)
			}
			if tc.wantHas && g.SavedRaw != tc.wantRaw {
				t.Fatalf("saved raw = %d, want %d", g.SavedRaw, tc.wantRaw)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("config.json was rewritten on load; before:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// readDefaultsMap returns agents.defaults from the config file on disk.
func readDefaultsMap(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m struct {
		Agents struct {
			Defaults map[string]any `json:"defaults"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return m.Agents.Defaults
}

// TestMaxToolIterationsEnvImport_Dataset covers Dataset "Env import" (no
// marker, file global 200): the value is saved to config.json with the marker
// (clamped to the nearest bound), a non-numeric or unset value writes nothing.
func TestMaxToolIterationsEnvImport_Dataset(t *testing.T) {
	cases := []struct {
		name       string
		env        string
		setEnv     bool
		wantGlobal int
		wantMarker bool
	}{
		{"1 in range", "80", true, 80, true},
		{"2 min", "1", true, 1, true},
		{"3 max", "1000", true, 1000, true},
		{"4 zero clamps to 1", "0", true, 1, true},
		{"5 above clamps to 1000", "5000", true, 1000, true},
		{"6 negative clamps to 1", "-7", true, 1, true},
		{"7 non-numeric", "abc", true, 200, false},
		{"8 unset", "", false, 200, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setEnv {
				t.Setenv(MaxToolIterationsEnvVar, tc.env)
			} else {
				t.Setenv(MaxToolIterationsEnvVar, "")
				os.Unsetenv(MaxToolIterationsEnvVar)
			}
			path, before := writeMaxToolIterConfig(t, `"max_tool_iterations": 200`)
			var healed []byte
			cfg, err := LoadConfigWithStoreAndSelfHealHook(path, nil, func(b []byte) { healed = b })
			if err != nil {
				t.Fatalf("LoadConfig must never refuse over the env var: %v", err)
			}
			if got := cfg.Agents.Defaults.EffectiveGlobalMaxToolIterations().Value; got != tc.wantGlobal {
				t.Fatalf("in-memory global = %d, want %d", got, tc.wantGlobal)
			}
			after, _ := os.ReadFile(path)
			if !tc.wantMarker {
				if !bytes.Equal(before, after) || healed != nil {
					t.Fatalf("config.json must be untouched; after:\n%s", after)
				}
				return
			}
			d := readDefaultsMap(t, path)
			if d["max_tool_iterations"] != float64(tc.wantGlobal) {
				t.Fatalf("saved max_tool_iterations = %v, want %d", d["max_tool_iterations"], tc.wantGlobal)
			}
			if d["max_tool_iterations_env_imported"] != true {
				t.Fatalf("marker not saved: %v", d)
			}
			if !bytes.Equal(healed, after) {
				t.Fatal("self-heal hook must receive the exact bytes written")
			}
			// Unrelated keys survive the raw-map patch.
			if _, ok := d["default_model"]; !ok {
				t.Fatal("default_model lost by the import write")
			}
		})
	}
}

// TestMaxToolIterationsEnvImport_IgnoredAfterMarker: once imported, a later
// saved value wins and the env var is never read as a value again (D5, US-7
// AS-2), and the file is not rewritten.
func TestMaxToolIterationsEnvImport_IgnoredAfterMarker(t *testing.T) {
	t.Setenv(MaxToolIterationsEnvVar, "80")
	path, before := writeMaxToolIterConfig(t,
		`"max_tool_iterations": 150, "max_tool_iterations_env_imported": true`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Agents.Defaults.EffectiveGlobalMaxToolIterations().Value; got != 150 {
		t.Fatalf("global = %d, want 150 (env var must be ignored after the marker)", got)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatalf("config.json rewritten although already imported:\n%s", after)
	}
}

// TestMaxToolIterationsEnv_NotALiveSource: the env tag is gone, so a fresh
// struct parse never picks the variable up through env.Parse (D5).
func TestMaxToolIterationsEnv_NotALiveSource(t *testing.T) {
	t.Setenv(MaxToolIterationsEnvVar, "80")
	path, _ := writeMaxToolIterConfig(t, `"max_tool_iterations": 150, "max_tool_iterations_env_imported": true`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 150 {
		t.Fatalf("raw saved global = %d, want 150 — env.Parse must not override it",
			cfg.Agents.Defaults.MaxToolIterations)
	}
}

// TestMaxToolIterationsValidate_Messages pins the spec's refusal texts
// (Machine-Verifiable Constraints) and the per-agent bounds dataset.
func TestMaxToolIterationsValidate_Messages(t *testing.T) {
	d := &AgentDefaults{MaxToolIterations: 1000}
	for _, n := range []int{1, 1000} {
		if err := ValidateAgentMaxToolIterations(n, d); err != nil {
			t.Fatalf("%d must be accepted with global 1000: %v", n, err)
		}
	}
	for _, n := range []int{0, -3, 1001} {
		err := ValidateAgentMaxToolIterations(n, d)
		if err == nil || err.Error() != "max_tool_iterations must be between 1 and 1000" {
			t.Fatalf("%d: got %v, want the bound message", n, err)
		}
	}
	err := ValidateAgentMaxToolIterations(300, &AgentDefaults{MaxToolIterations: 200})
	want := "max_tool_iterations 300 is above the global limit (200); lower it, or raise the global limit in Settings → Performance"
	if err == nil || err.Error() != want {
		t.Fatalf("above global: got %v, want %q", err, want)
	}
	if err := ValidateAgentMaxToolIterations(200, &AgentDefaults{MaxToolIterations: 200}); err != nil {
		t.Fatalf("equal to global must be accepted: %v", err)
	}
	if err := ValidateMaxToolIterationsBound(1001); err == nil {
		t.Fatal("global bound must refuse 1001")
	}
}

// TestMaxToolIterationsCappedAgents_OneLine: the D19 startup warning is a
// single line naming every capped agent with its stored value and the global,
// and never naming an agent at or below the global.
func TestMaxToolIterationsCappedAgents_OneLine(t *testing.T) {
	d := &AgentDefaults{MaxToolIterations: 200}
	agents := []AgentConfig{
		{ID: "agent-a", MaxToolIterations: 500},
		{ID: "agent-b", MaxToolIterations: 300},
		{ID: "agent-c", MaxToolIterations: 100},
		{ID: "agent-d"},
	}
	msg, fields, ok := cappedAgentsWarning(d, agents)
	if !ok {
		t.Fatal("expected a warning for capped agents")
	}
	if strings.Contains(msg, "\n") {
		t.Fatalf("warning must be one line: %q", msg)
	}
	for _, want := range []string{"agent-a (stored 500)", "agent-b (stored 300)", "(200)"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("warning %q missing %q", msg, want)
		}
	}
	for _, not := range []string{"agent-c", "agent-d"} {
		if strings.Contains(msg, not) {
			t.Fatalf("warning %q must not name %s", msg, not)
		}
	}
	if fields["count"] != 2 || fields["global"] != 200 {
		t.Fatalf("fields = %v, want count 2, global 200", fields)
	}
	if _, _, ok := cappedAgentsWarning(d, agents[2:]); ok {
		t.Fatal("no warning expected when no agent is capped")
	}
}
