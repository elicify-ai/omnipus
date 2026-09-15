// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Audit logging is ON by default (founder decision, 2026-09-11). These tests
// pin the three things that decision actually needs to be true, because a
// default-value change is exactly the kind that gets "tested" by assertions
// that would pass either way:
//
//  1. a fresh install has it on,
//  2. an install whose config.json predates the change gains it on next load,
//  3. an operator who turns it OFF stays off — across a save/load round-trip,
//     which is where the `omitempty` that used to sit on this field would
//     silently resurrect it.
//
// The provenance flag (AuditLogFromDefault) is pinned alongside, because the
// abort-vs-degrade decision in pkg/agent/loop.go's audit-construction block
// reads it and nothing else. Note its polarity: TRUE means "this value is the
// shipped default, nobody wrote it", which selects the degrade branch; FALSE
// means somebody set it, which selects the fail-closed boot abort.

// minimalConfigJSON returns a load-valid config.json body with the given
// `sandbox` section spliced in verbatim, so each test varies exactly one
// thing: whether (and how) `audit_log` appears.
//
// The surrounding shape is copied from the CI seed pinned by
// TestCISeedConfig_LoadsSuccessfully — an explicit `provider` alongside a bare
// `model` id — so these tests fail for audit-log reasons only, never because
// the config could not load at all.
func minimalConfigJSON(sandboxSection string) string {
	return `{
  "version": ` + currentVersionJSON() + `,
  "sandbox": ` + sandboxSection + `,
  "agents": { "defaults": { "default_model": { "provider": "openrouter", "model": "z-ai/glm-5.2" } } },
  "providers": [
    {
      "provider": "openrouter",
      "model": "z-ai/glm-5.2",
      "api_base": "https://openrouter.ai/api/v1",
      "api_key_ref": "OPENROUTER_API_KEY"
    }
  ]
}`
}

func currentVersionJSON() string {
	b, _ := json.Marshal(CurrentVersion)
	return string(b)
}

// TestDefaultConfig_AuditLogIsOnByDefault is the fresh-install case at its
// source. DefaultConfig() is what loadConfigInternal returns verbatim when
// there is no config.json on disk at all — a brand-new install — and it is
// also the base every operator config.json is unmarshalled over.
func TestDefaultConfig_AuditLogIsOnByDefault(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Sandbox.AuditLog {
		t.Error("DefaultConfig().Sandbox.AuditLog = false, want true — " +
			"a fresh install must record who changed what from its first minute (SEC-15/SEC-17)")
	}
	if !cfg.Sandbox.AuditLogFromDefault {
		t.Error("DefaultConfig().Sandbox.AuditLogFromDefault = false, want true — " +
			"the seeded value is not an operator request, and pkg/agent/loop.go reads this flag " +
			"to decide whether an audit-construction failure aborts boot or degrades")
	}
}

// TestLoadConfig_FreshInstall_NoConfigFile_HasAuditOn drives the real
// LoadConfig entry point against a path that does not exist, which is the
// literal fresh-install boot path.
func TestLoadConfig_FreshInstall_NoConfigFile_HasAuditOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist", "config.json")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig on a missing file must fall back to defaults, got error: %v", err)
	}
	if !cfg.Sandbox.AuditLog {
		t.Error("fresh install (no config.json) resolved audit_log = false, want true")
	}
	if !cfg.Sandbox.AuditLogFromDefault {
		t.Error("fresh install (no config.json) did not mark audit_log as coming from the default; " +
			"nobody wrote a config.json at all, so it cannot be an explicit request")
	}
}

// TestLoadConfig_UpgradeWithoutAuditLogKey_GainsAudit is the upgrade case.
//
// An install whose config.json was written before this change has a `sandbox`
// block but no `audit_log` key inside it. Because loadConfig unmarshals the
// operator's JSON over DefaultConfig(), the absent key must resolve to the
// seeded true — the same additive self-heal-forward behaviour ADR-076 gives
// the tool-policy ceiling. The `sandbox` block here is deliberately NON-empty
// so the test proves the key is what is missing, not the whole section.
func TestLoadConfig_UpgradeWithoutAuditLogKey_GainsAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := minimalConfigJSON(`{ "mode": "enforce", "tool_policies": { "spawn": "allow" } }`)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if !cfg.Sandbox.AuditLog {
		t.Error("a config.json with a sandbox block but no audit_log key resolved to false; " +
			"want true — an upgraded install must gain audit on next load, not stay silently off")
	}
	if !cfg.Sandbox.AuditLogFromDefault {
		t.Error("audit_log was absent from the file but was not marked as coming from the default")
	}
	// Guard the guard: if the sandbox block failed to load at all, the
	// assertion above would pass for the wrong reason.
	if cfg.Sandbox.Mode != SandboxModeEnforce {
		t.Fatalf("sandbox block did not load (Mode = %q, want %q) — "+
			"the audit_log assertion above would have passed vacuously",
			cfg.Sandbox.Mode, SandboxModeEnforce)
	}
}

// TestLoadConfig_ExplicitAuditLogIsRespected covers both explicit values. The
// `false` half is the one that matters: the seed must never overwrite a
// decision the operator actually made.
func TestLoadConfig_ExplicitAuditLogIsRespected(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		want bool
	}{
		{"explicit false", `{ "audit_log": false }`, false},
		{"explicit true", `{ "audit_log": true }`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(minimalConfigJSON(tc.json)), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.Sandbox.AuditLog != tc.want {
				t.Errorf("audit_log = %v, want %v — an explicit operator value must win over the seed",
					cfg.Sandbox.AuditLog, tc.want)
			}
			if cfg.Sandbox.AuditLogFromDefault {
				t.Errorf("audit_log was physically written in config.json as %v, but the config still "+
					"reports it as coming from the default; pkg/agent/loop.go would then degrade "+
					"instead of failing closed on an audit-construction failure", tc.want)
			}
		})
	}
}

// TestSaveConfig_ExplicitAuditLogFalseSurvivesRoundTrip is the omitempty trap,
// and it is the reason `audit_log` carries no `omitempty` tag.
//
// With a default of true, `json:"audit_log,omitempty"` makes the operator's
// `false` unrepresentable on the whole-struct marshal path: SaveConfig does
// json.MarshalIndent(cfg), omitempty drops the false, and the next LoadConfig
// reads the now-absent key as the seeded true — audit switches itself back on
// behind the operator's back. That path is live in production (pkg/gateway
// wires SaveConfig in as the sysagent tools' SaveConfigLocked), so this is a
// reachable silent reversal, not a theoretical one.
//
// Re-adding omitempty to the field makes this test fail.
func TestSaveConfig_ExplicitAuditLogFalseSurvivesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := DefaultConfig()
	cfg.Sandbox.AuditLog = false

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// The key must be physically present in the file. Asserting only on the
	// reloaded value would not distinguish "false was written" from "the key
	// vanished and something else happened to yield false".
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(raw), `"audit_log"`) {
		t.Fatal(`saved config.json has no "audit_log" key — omitempty dropped the operator's false; ` +
			`the next load will resurrect the seeded true and silently re-enable audit`)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after SaveConfig: %v", err)
	}
	if reloaded.Sandbox.AuditLog {
		t.Error("audit_log came back true after a save/load round-trip of an explicit false — " +
			"the operator's off-switch does not stick")
	}
	if reloaded.Sandbox.AuditLogFromDefault {
		t.Error("audit_log round-tripped as a value but is still reported as coming from the default")
	}
}

// TestAuditLogFromDefault_IsNotSettableFromOperatorJSON pins the `json:"-"`
// tag. The flag is provenance, not configuration: if operator JSON could set
// it, an operator — or an agent editing config.json — could flip a config that
// really does request audit into one that merely defaults to it, downgrading a
// fail-closed boot abort into a silent degrade. It must be derivable only from
// whether the audit_log key is physically present.
func TestAuditLogFromDefault_IsNotSettableFromOperatorJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	// Write audit_log explicitly (so the honest answer is "not from default")
	// while also trying to claim the opposite through every plausible spelling.
	body := minimalConfigJSON(
		`{ "audit_log": true, "AuditLogFromDefault": true, "audit_log_from_default": true }`)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Sandbox.AuditLogFromDefault {
		t.Error("AuditLogFromDefault was set from operator JSON; it must be derived only from " +
			"the presence of the audit_log key (json:\"-\")")
	}

	// And it must never be written back out.
	out := filepath.Join(t.TempDir(), "out.json")
	cfg.Sandbox.AuditLogFromDefault = true
	if saveErr := SaveConfig(out, cfg); saveErr != nil {
		t.Fatalf("SaveConfig: %v", saveErr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lowered := strings.ToLower(string(raw))
	if strings.Contains(lowered, "auditlogfromdefault") ||
		strings.Contains(lowered, "audit_log_from_default") {
		t.Error("AuditLogFromDefault leaked into the serialized config.json; it is transient provenance")
	}
}
