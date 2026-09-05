// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// workspace_path_guard_test.go — ADR-068 §6 coverage for the operator-facing
// control over the in-process path guard.
//
// Background. AgentDefaults.RestrictToWorkspace is the ADR-068 "third rule
// layer": a Go-side path check that runs before any child process is spawned
// and is completely separate from the kernel sandbox (`sandbox.mode`). Until
// ADR-068 it was reachable only through an environment variable, and
// validateRemovedKeys REJECTS any config.json carrying
// `agents.defaults.restrict_to_workspace`. Operators therefore had a rule
// they could not see and could not change, which is the operator-experience
// defect ADR-068 §6 records.
//
// The control is now `sandbox.workspace_path_guard`, resolved into
// RestrictToWorkspace at load time by applyWorkspacePathGuard. These tests
// pin the four things that could silently break:
//
//  1. the tri-state semantics (unset / true / false) and the safe default;
//  2. the precedence order env > config key > default, including the
//     empty-env-var edge that env.Parse itself ignores;
//  3. the SaveConfig → LoadConfig round-trip, in BOTH directions and across
//     two cycles, so an explicit "off" is not silently re-enabled;
//  4. that none of this reopens the FR-001 removed-keys contract or needs a
//     config version bump.

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeConfigJSON marshals raw into cfgPath. Used by the tests that must
// start from ON-DISK JSON (rather than a Config struct) because what is
// being asserted is how a particular set of keys is READ.
func writeConfigJSON(t *testing.T, cfgPath string, raw map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatalf("marshaling test config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
}

// clearGuardEnv removes the env override for the duration of one test so a
// stray variable in the developer's shell cannot make these assertions pass
// or fail for the wrong reason. t.Setenv also fails the test if it is called
// from a parallel test, which is the behaviour we want here.
func clearGuardEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envRestrictToWorkspace, "")
	if err := os.Unsetenv(envRestrictToWorkspace); err != nil {
		t.Fatalf("unsetting %s: %v", envRestrictToWorkspace, err)
	}
}

// ---------------------------------------------------------------------------
// Tri-state semantics and the safe default
// ---------------------------------------------------------------------------

// TestWorkspacePathGuard_UnsetKeepsGuardOn asserts the shipped default. A nil
// pointer means "no operator has ever expressed an opinion", and the guard
// must stay ON — this is the fail-closed direction, and it is what every
// existing install gets when it upgrades into a build that has this key.
//
// It also asserts the pointer is still nil afterwards: applyWorkspacePathGuard
// must NOT write the resolved value back, or the next SaveConfig would grow a
// key the operator never set.
func TestWorkspacePathGuard_UnsetKeepsGuardOn(t *testing.T) {
	clearGuardEnv(t)

	cfg := DefaultConfig()
	if cfg.Sandbox.WorkspacePathGuard != nil {
		t.Fatalf("DefaultConfig must leave WorkspacePathGuard nil (unset), got %v",
			*cfg.Sandbox.WorkspacePathGuard)
	}

	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("validateBootConfig: %v", err)
	}

	if !cfg.Agents.Defaults.RestrictToWorkspace {
		t.Error("an unset sandbox.workspace_path_guard must leave the guard ON (fail-closed default)")
	}
	if cfg.Sandbox.WorkspacePathGuard != nil {
		t.Error("applyWorkspacePathGuard must not materialize the pointer for an unset key — " +
			"doing so would write sandbox.workspace_path_guard into every config.json on the next save")
	}
}

// TestWorkspacePathGuard_ExplicitFalseTurnsGuardOff is the whole point of
// ADR-068 §6: an operator can now switch this off without an env var.
func TestWorkspacePathGuard_ExplicitFalseTurnsGuardOff(t *testing.T) {
	clearGuardEnv(t)

	cfg := DefaultConfig()
	off := false
	cfg.Sandbox.WorkspacePathGuard = &off

	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("validateBootConfig: %v", err)
	}

	if cfg.Agents.Defaults.RestrictToWorkspace {
		t.Error("sandbox.workspace_path_guard=false must resolve RestrictToWorkspace to false")
	}
}

// TestWorkspacePathGuard_ExplicitTrueTurnsGuardOn covers the direction that a
// plain bool could never express: the operator turning the guard back ON from
// a state where the struct field had already been set to false. If the
// resolver only ever applied `false` (an easy way to write this bug), this
// test is what catches it.
func TestWorkspacePathGuard_ExplicitTrueTurnsGuardOn(t *testing.T) {
	clearGuardEnv(t)

	cfg := DefaultConfig()
	cfg.Agents.Defaults.RestrictToWorkspace = false // pretend a prior state
	on := true
	cfg.Sandbox.WorkspacePathGuard = &on

	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("validateBootConfig: %v", err)
	}

	if !cfg.Agents.Defaults.RestrictToWorkspace {
		t.Error("sandbox.workspace_path_guard=true must resolve RestrictToWorkspace to true")
	}
}

// ---------------------------------------------------------------------------
// Precedence: env var > config key > default
// ---------------------------------------------------------------------------

// TestWorkspacePathGuard_EnvBeatsConfigKey pins the FR-001 ops escape hatch.
// The env var must keep winning in BOTH directions — it is the only way to
// recover an install whose config.json locks the operator out, so a test that
// only checked "env can turn it off" would miss half the contract.
func TestWorkspacePathGuard_EnvBeatsConfigKey(t *testing.T) {
	cases := []struct {
		name      string
		envValue  string
		configKey bool
		want      bool
	}{
		{name: "env false beats config true", envValue: "false", configKey: true, want: false},
		{name: "env true beats config false", envValue: "true", configKey: false, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envRestrictToWorkspace, tc.envValue)

			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.json")
			writeConfigJSON(t, cfgPath, map[string]any{
				"version": CurrentVersion,
				"sandbox": map[string]any{"workspace_path_guard": tc.configKey},
			})

			loaded, err := LoadConfig(cfgPath)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if loaded.Agents.Defaults.RestrictToWorkspace != tc.want {
				t.Errorf("RestrictToWorkspace: got %v, want %v (env=%q must beat config key=%v)",
					loaded.Agents.Defaults.RestrictToWorkspace, tc.want, tc.envValue, tc.configKey)
			}
		})
	}
}

// TestWorkspacePathGuard_EmptyEnvVarDoesNotVoidConfigKey covers the edge that
// makes os.LookupEnv alone the wrong test. caarlos0/env skips an env var whose
// value is the empty string (`if value != ""` in setField), so an empty
// variable overrides NOTHING. If applyWorkspacePathGuard treated mere
// presence as "the operator decided", an exported-but-empty variable would
// silently void the operator's config key while appearing to do nothing.
func TestWorkspacePathGuard_EmptyEnvVarDoesNotVoidConfigKey(t *testing.T) {
	t.Setenv(envRestrictToWorkspace, "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeConfigJSON(t, cfgPath, map[string]any{
		"version": CurrentVersion,
		"sandbox": map[string]any{"workspace_path_guard": false},
	})

	loaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Agents.Defaults.RestrictToWorkspace {
		t.Error("an EMPTY env var overrides nothing in env.Parse, so the config key must still apply")
	}
}

// ---------------------------------------------------------------------------
// Save / load round-trip
// ---------------------------------------------------------------------------

// TestWorkspacePathGuard_RoundTripsAcrossTwoCycles is the regression guard for
// the failure mode a plain bool would have: an explicit "off" being read back
// as "unset" and silently re-defaulted to ON. Two full cycles, because the
// first save is not where that bug shows up — the second one is.
func TestWorkspacePathGuard_RoundTripsAcrossTwoCycles(t *testing.T) {
	clearGuardEnv(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	off := false
	cfg.Sandbox.WorkspacePathGuard = &off

	if err := SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	first, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig (cycle 1): %v", err)
	}
	if first.Sandbox.WorkspacePathGuard == nil {
		t.Fatal("sandbox.workspace_path_guard=false was dropped by the first SaveConfig/LoadConfig cycle")
	}
	if *first.Sandbox.WorkspacePathGuard {
		t.Fatalf("cycle 1: got workspace_path_guard=true, want false")
	}
	if first.Agents.Defaults.RestrictToWorkspace {
		t.Error("cycle 1: RestrictToWorkspace must resolve to false")
	}

	// Cycle 2: save what was loaded, exactly as the gateway does on every
	// config reload (LoadConfig may normalize and re-save).
	if err = SaveConfig(cfgPath, first); err != nil {
		t.Fatalf("SaveConfig (cycle 2): %v", err)
	}
	second, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig (cycle 2): %v", err)
	}
	if second.Sandbox.WorkspacePathGuard == nil || *second.Sandbox.WorkspacePathGuard {
		t.Error("cycle 2: an explicit workspace_path_guard=false must survive a second round-trip, not " +
			"revert to the ON default")
	}
	if second.Agents.Defaults.RestrictToWorkspace {
		t.Error("cycle 2: RestrictToWorkspace must still resolve to false")
	}
}

// TestWorkspacePathGuard_UnsetIsNotSerialized asserts that an untouched
// install never acquires the key. `omitempty` on a *bool is what delivers
// this; dropping the tag would make every existing config.json grow a
// security key nobody set, which is noise at best and a silent posture
// declaration at worst.
func TestWorkspacePathGuard_UnsetIsNotSerialized(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	if err := SaveConfig(cfgPath, DefaultConfig()); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading saved config: %v", err)
	}
	if strings.Contains(string(raw), "workspace_path_guard") {
		t.Error("an unset workspace path guard must not be written to config.json")
	}
}

// ---------------------------------------------------------------------------
// The FR-001 removed-keys contract is untouched
// ---------------------------------------------------------------------------

// TestWorkspacePathGuard_DoesNotReintroduceRemovedKeys is the self-bricking
// guard. If anyone ever "simplifies" this by giving RestrictToWorkspace a
// real JSON tag, SaveConfig would write agents.defaults.restrict_to_workspace
// into config.json and the very next LoadConfig would reject that same file —
// a one-boot brick, because the gateway re-saves config on load.
//
// The test writes a config with the guard explicitly OFF (the value someone
// would be tempted to persist under the old name), saves it, and asserts both
// that the removed keys are absent from the bytes AND that the file still
// loads.
func TestWorkspacePathGuard_DoesNotReintroduceRemovedKeys(t *testing.T) {
	clearGuardEnv(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	off := false
	cfg.Sandbox.WorkspacePathGuard = &off
	if err := SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading saved config: %v", err)
	}
	for _, removed := range []string{"restrict_to_workspace", "allow_read_outside_workspace"} {
		if strings.Contains(string(raw), removed) {
			t.Errorf("SaveConfig wrote the FR-001 removed key %q — the next LoadConfig would reject "+
				"this file and brick the install", removed)
		}
	}

	// The saved file must still load. validateRemovedKeys runs before
	// unmarshal, so this is the assertion that proves the round-trip is safe.
	if _, loadErr := LoadConfig(cfgPath); loadErr != nil {
		t.Fatalf("a config saved with workspace_path_guard set must still load, got: %v", loadErr)
	}
}

// TestWorkspacePathGuard_RemovedKeysStillRejected asserts the new key did not
// relax the old contract. The exact FR-001 message is part of that contract
// and is compared byte-for-byte, as elsewhere.
func TestWorkspacePathGuard_RemovedKeysStillRejected(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "removed key alone",
			raw:  `{"version":2,"agents":{"defaults":{"restrict_to_workspace":false}}}`,
		},
		{
			name: "removed key alongside the new one",
			raw: `{"version":2,"sandbox":{"workspace_path_guard":false},` +
				`"agents":{"defaults":{"restrict_to_workspace":false}}}`,
		},
		{
			name: "read-side removed key alongside the new one",
			raw: `{"version":2,"sandbox":{"workspace_path_guard":true},` +
				`"agents":{"defaults":{"allow_read_outside_workspace":true}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRemovedKeys([]byte(tc.raw))
			if err == nil {
				t.Fatal("expected the FR-001 removed-keys rejection, got nil")
			}
			if err.Error() != fr001RemovedKeysMsg {
				t.Fatalf("expected the exact FR-001 message, got: %q", err.Error())
			}
		})
	}
}

// TestWorkspacePathGuard_NewKeyAloneIsAccepted is the other half of the pair
// above: the new key must NOT be caught by validateRemovedKeys. Without this,
// a future tightening of that validator could quietly make the new control
// unloadable and the only symptom would be a boot failure in the field.
func TestWorkspacePathGuard_NewKeyAloneIsAccepted(t *testing.T) {
	raw := []byte(`{"version":2,"sandbox":{"workspace_path_guard":false},"agents":{"defaults":{}}}`)
	if err := validateRemovedKeys(raw); err != nil {
		t.Fatalf("sandbox.workspace_path_guard must not trip the FR-001 check, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// No migration required
// ---------------------------------------------------------------------------

// TestWorkspacePathGuard_PreExistingConfigNeedsNoMigration pins the claim that
// this change needs no config version bump. A config.json written before the
// key existed — same CurrentVersion, no sandbox.workspace_path_guard — must
// load without error and come up with the guard ON.
//
// If this ever requires a version bump, this test is where that shows up: it
// would start failing with "unsupported config version" rather than silently
// changing an existing install's security posture.
func TestWorkspacePathGuard_PreExistingConfigNeedsNoMigration(t *testing.T) {
	clearGuardEnv(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeConfigJSON(t, cfgPath, map[string]any{
		"version": CurrentVersion,
		"sandbox": map[string]any{"filesystem_model": "open"},
		"agents":  map[string]any{"defaults": map[string]any{"workspace": dir}},
	})

	loaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("a config predating sandbox.workspace_path_guard must load unchanged, got: %v", err)
	}
	if loaded.Sandbox.WorkspacePathGuard != nil {
		t.Error("absent key must stay nil, not be materialized on load")
	}
	if !loaded.Agents.Defaults.RestrictToWorkspace {
		t.Error("an upgraded install must keep the guard ON — this key must never widen an existing " +
			"install's posture by being introduced")
	}
}

// ---------------------------------------------------------------------------
// Structural guards against name drift
// ---------------------------------------------------------------------------

// TestWorkspacePathGuard_EnvNameMatchesStructTag ties the constant that
// applyWorkspacePathGuard reads with os.LookupEnv to the `env:` tag that
// env.Parse reads. Two independent spellings of one variable name is exactly
// the kind of drift that produces a precedence rule which silently stops
// working.
func TestWorkspacePathGuard_EnvNameMatchesStructTag(t *testing.T) {
	field, ok := reflect.TypeOf(AgentDefaults{}).FieldByName("RestrictToWorkspace")
	if !ok {
		t.Fatal("AgentDefaults.RestrictToWorkspace no longer exists — ADR-068 §6 resolves into this field")
	}
	if got := field.Tag.Get("env"); got != envRestrictToWorkspace {
		t.Fatalf("env tag %q != envRestrictToWorkspace %q — applyWorkspacePathGuard's precedence check "+
			"would read a variable env.Parse never applies", got, envRestrictToWorkspace)
	}
	// The JSON tag must stay "-": a real tag here is the self-bricking change
	// TestWorkspacePathGuard_DoesNotReintroduceRemovedKeys describes.
	if got := field.Tag.Get("json"); got != "-" {
		t.Fatalf("AgentDefaults.RestrictToWorkspace must keep json:\"-\" (FR-001), got %q", got)
	}
}

// TestWorkspacePathGuard_ConfigKeyMatchesJSONTag ties the ConfigKey constant
// that the gateway's blocked-path and audit surfaces use to the actual JSON
// tag. A rename of one without the other would leave the control writable
// through an unaudited endpoint, or audited under a path that does not exist.
func TestWorkspacePathGuard_ConfigKeyMatchesJSONTag(t *testing.T) {
	field, ok := reflect.TypeOf(OmnipusSandboxConfig{}).FieldByName("WorkspacePathGuard")
	if !ok {
		t.Fatal("OmnipusSandboxConfig.WorkspacePathGuard no longer exists (ADR-068 §6)")
	}
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if want := "sandbox." + name; string(SandboxWorkspacePathGuard) != want {
		t.Fatalf("SandboxWorkspacePathGuard=%q but the field serializes as %q",
			SandboxWorkspacePathGuard, want)
	}
	if !strings.Contains(field.Tag.Get("json"), "omitempty") {
		t.Error("WorkspacePathGuard must keep omitempty so an unset guard is never written to config.json")
	}
	if field.Type.Kind() != reflect.Pointer {
		t.Error("WorkspacePathGuard must stay a *bool — a plain bool cannot distinguish " +
			"\"operator chose false\" from \"never set\"")
	}
}

// TestWorkspacePathGuard_IsUnderTheSandboxNamespace records WHY the key lives
// under `sandbox.` rather than beside the field it drives. Everything under
// that prefix is blocked from the generic PUT /api/v1/config endpoint and from
// the sysagent's own system.config.set tool, so the control can only be
// changed through the admin-gated, re-auth-gated, audited sandbox-config
// endpoint. Moving it to `agents.defaults.*` would make it writable by an
// agent — i.e. an agent could switch off its own cage.
func TestWorkspacePathGuard_IsUnderTheSandboxNamespace(t *testing.T) {
	if !strings.HasPrefix(string(SandboxWorkspacePathGuard), "sandbox.") {
		t.Fatalf("SandboxWorkspacePathGuard=%q must stay under the sandbox namespace: that prefix is what "+
			"blocks it from the unaudited generic config endpoint and from system.config.set",
			SandboxWorkspacePathGuard)
	}
	// It must NOT be the kernel sandbox switch. ADR-068 §6 requires the two to
	// be labelled distinctly; sharing a key would make that impossible.
	if SandboxWorkspacePathGuard == SandboxModeKey {
		t.Fatal("the workspace path guard and the kernel sandbox mode must remain separate keys")
	}
}
