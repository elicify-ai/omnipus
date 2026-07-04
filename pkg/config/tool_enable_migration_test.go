// Contract test: the tools.<name>.enabled=false → security.tool_policies deny
// migration (migrateDeprecatedToolEnableFlags, migration.go) must be a true
// one-time migration, not a "re-derive on every load" shim.
//
// Bug: the migration translated a legacy enabled=false flag into an in-memory
// deny policy on EVERY load/hot-reload, but never removed the legacy flag and
// never persisted the derived policy. When an operator used Settings →
// Security to set such a tool to "allow", the frontend deleted the (now
// redundant, since allow==default) tool_policies override, the PUT persisted
// the deletion correctly, then TriggerReload re-ran the migration — which saw
// the legacy flag was still present and re-injected "deny", silently
// bouncing the operator's choice back on every refresh.
//
// Fix: stripDeprecatedToolEnableFlagsOnDisk (tool_enable_migration.go) runs
// once per legacy flag, persisting the derived policy as a real
// sandbox.tool_policies entry and removing the legacy tools.<name>.enabled
// key from disk, so the migration has nothing left to re-derive from on the
// next load.
//
// Traces to: pkg/config/migration.go (migrateDeprecatedToolEnableFlags),
// pkg/config/tool_enable_migration.go (stripDeprecatedToolEnableFlagsOnDisk).

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadConfig_ToolEnableFlags_PersistedAndStripped is the PRIMARY
// regression test. Given tools.spawn_status.enabled=false and
// tools.mcp.enabled=false, after a single LoadConfigWithStoreAndSelfHealHook:
//   - cfg.Sandbox.ToolPolicies["delegate"] == "deny" (in memory)
//   - cfg.Sandbox.ToolPolicies["mcp_*"] == "deny" (in memory)
//   - the on-disk config.json no longer carries tools.spawn_status.enabled or
//     tools.mcp.enabled
//   - the on-disk config.json carries the deny entries as REAL persisted
//     sandbox.tool_policies entries
//   - the self-heal write hook fired exactly once
func TestLoadConfig_ToolEnableFlags_PersistedAndStripped(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"tools": {
			"spawn_status": {"enabled": false},
			"mcp": {"enabled": false}
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	var hookCalls int
	var hookBytes []byte
	cfg, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func(writtenBytes []byte) {
		hookCalls++
		hookBytes = writtenBytes
	})
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// In-memory: both legacy flags translated to deny.
	require.NotNil(t, cfg.Sandbox.ToolPolicies)
	assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["delegate"])
	assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["mcp_*"])

	// Self-heal fired exactly once for this load (a single write covers both flags).
	assert.Equal(t, 1, hookCalls, "onSelfHeal must fire exactly once when the tool-enable-flag self-heal writes")
	require.NotNil(t, hookBytes)

	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	assert.Equal(t, string(onDisk), string(hookBytes),
		"the hook must receive the exact bytes written to disk, so the caller's hash registration matches")

	var m map[string]any
	require.NoError(t, json.Unmarshal(onDisk, &m))

	toolsRaw, ok := m["tools"].(map[string]any)
	require.True(t, ok, "tools section must still exist on disk")

	spawnStatus, ok := toolsRaw["spawn_status"].(map[string]any)
	require.True(t, ok)
	_, hasEnabled := spawnStatus["enabled"]
	assert.False(t, hasEnabled, "tools.spawn_status.enabled must be GONE from the reserialized config")

	mcp, ok := toolsRaw["mcp"].(map[string]any)
	require.True(t, ok)
	_, hasEnabled = mcp["enabled"]
	assert.False(t, hasEnabled, "tools.mcp.enabled must be GONE from the reserialized config")

	sandboxRaw, ok := m["sandbox"].(map[string]any)
	require.True(t, ok, "sandbox section must exist on disk after the self-heal write")
	policies, ok := sandboxRaw["tool_policies"].(map[string]any)
	require.True(t, ok, "sandbox.tool_policies must exist on disk after the self-heal write")
	assert.Equal(t, "deny", policies["delegate"],
		"delegate must be a REAL persisted deny entry, not just an in-memory derivation")
	assert.Equal(t, "deny", policies["mcp_*"],
		"mcp_* must be a REAL persisted deny entry, not just an in-memory derivation")
}

// TestLoadConfig_ToolEnableFlags_AllowSticksAcrossReload is the acceptance
// test for the actual operator-facing bug: after the legacy flag has been
// migrated once (real deny entry persisted, legacy flag stripped), simulate
// an operator flipping the tool to "allow" via the Settings UI — which
// deletes the tool_policies override, since allow is the policy engine's
// default — and reload. The key must NOT be re-injected as deny, because the
// legacy flag that used to resurrect it is gone.
func TestLoadConfig_ToolEnableFlags_AllowSticksAcrossReload(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"tools": {
			"spawn_status": {"enabled": false}
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	// First load: migrates + persists the deny entry and strips the legacy flag.
	cfg1, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "deny", cfg1.Sandbox.ToolPolicies["delegate"])

	// Simulate the frontend/PUT handler behavior: operator sets the tool to
	// "allow" in Settings -> Security. Since allow is the policy engine's
	// default, the ToolPolicyEditor deletes the override entirely rather than
	// writing an explicit "allow" — this is exactly what made the bug
	// unfixable via the UI before this fix (the frontend never keeps an
	// explicit entry that could block re-injection).
	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	var m map[string]any
	require.NoError(t, json.Unmarshal(onDisk, &m))
	sandboxRaw, ok := m["sandbox"].(map[string]any)
	require.True(t, ok)
	policies, ok := sandboxRaw["tool_policies"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, policies, "delegate")
	delete(policies, "delegate")
	patched, marshalErr := json.MarshalIndent(m, "", "  ")
	require.NoError(t, marshalErr)
	require.NoError(t, os.WriteFile(configPath, patched, 0o600))

	// Reload (mirrors TriggerReload / the file-watcher poll picking up the PUT).
	deprecatedToolEnableMigrateOnce = sync.Once{}
	cfg2, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, nil)
	require.NoError(t, err)

	// REGRESSION ASSERTION: delegate must NOT be re-injected as
	// deny — the legacy flag that used to resurrect it was stripped by the
	// first load, so the migration has nothing left to detect.
	_, stillDenied := cfg2.Sandbox.ToolPolicies["delegate"]
	assert.False(t, stillDenied,
		"delegate must resolve to the policy-engine default (allow) after the operator "+
			"cleared the override — it must not bounce back to deny on reload")
}

// TestLoadConfig_ToolEnableFlags_Idempotent_NoOpNoRewrite verifies that a
// config with no legacy tools.<name>.enabled flags is a complete no-op: no
// write to disk (byte-for-byte identical before/after) and no policy change.
func TestLoadConfig_ToolEnableFlags_Idempotent_NoOpNoRewrite(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"tools": {
			"browser": {"max_tabs": 3}
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	before, err := os.ReadFile(configPath)
	require.NoError(t, err)

	hookCalled := false
	cfg, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func([]byte) {
		hookCalled = true
	})
	require.NoError(t, err)
	assert.False(t, hookCalled, "onSelfHeal must not fire when there are no legacy enabled flags to migrate")

	if cfg.Sandbox.ToolPolicies != nil {
		_, hasBrowser := cfg.Sandbox.ToolPolicies["browser_*"]
		assert.False(t, hasBrowser, "no policy entries must be derived when no legacy flag is present")
	}

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"no migration write should occur when there are no legacy tools.<name>.enabled=false flags")
}

// TestLoadConfig_ToolEnableFlags_DoesNotDowngradeExistingPolicy verifies that
// a pre-existing stricter-or-equal policy (e.g. "ask") is never overwritten
// to "deny" by the migration, while the now-redundant legacy flag is still
// removed from disk (it no longer carries any operator intent that isn't
// already captured by the explicit policy entry).
//
// Retargeted for ADR-036 (bash-tool-spec.md FR-M2): toolEnableToPolicy's
// {"exec", ...} row now maps to the "bash" glob, not "exec" — and
// migrateShellToolPolicyKeys (shell_tool_policy_migration.go, FR-M1) converts
// the pre-existing sandbox.tool_policies["exec"]="ask" entry itself into
// "bash" (running before the tools.exec.enabled=false migration, per the
// ordering comment in loadConfigInternal) so that migration's no-clobber
// guard sees "bash" already set to "ask" and never downgrades it to "deny".
// The net effect an operator observes is identical to before this ADR
// (their explicit "ask" survives, the legacy flag is gone) — only the key
// name the policy now lives under has changed.
func TestLoadConfig_ToolEnableFlags_DoesNotDowngradeExistingPolicy(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"tools": {
			"exec": {"enabled": false}
		},
		"sandbox": {
			"tool_policies": {"exec": "ask"}
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, nil)
	require.NoError(t, err)

	// In-memory: "ask" must survive under "bash", not be downgraded to "deny".
	assert.Equal(t, "ask", cfg.Sandbox.ToolPolicies["bash"])
	_, stillHasExec := cfg.Sandbox.ToolPolicies["exec"]
	assert.False(t, stillHasExec, "the legacy \"exec\" tool-policy key must be gone in memory after migration")

	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	var m map[string]any
	require.NoError(t, json.Unmarshal(onDisk, &m))

	toolsRaw, ok := m["tools"].(map[string]any)
	require.True(t, ok)
	execRaw, ok := toolsRaw["exec"].(map[string]any)
	require.True(t, ok)
	_, hasEnabled := execRaw["enabled"]
	assert.False(t, hasEnabled, "the redundant legacy tools.exec.enabled flag must still be removed on disk")

	sandboxRaw, ok := m["sandbox"].(map[string]any)
	require.True(t, ok)
	policies, ok := sandboxRaw["tool_policies"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ask", policies["bash"], "the pre-existing \"ask\" policy must survive as \"bash\", not be overwritten to \"deny\", on disk")
	_, hasLegacyExec := policies["exec"]
	assert.False(t, hasLegacyExec, "the legacy \"exec\" sandbox.tool_policies key must be deleted on disk, not left dangling")
}
