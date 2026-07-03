//go:build !cgo

// admin_only_roles_test.go — single-user model tests.
//
// Omnipus is now a single-user, self-hosted instance — there are no admin
// and normal users anymore (operator directive, 2026-07). The RBAC
// scaffolding (UserRole type, RequireAdmin middleware, the Users management
// screen, the ownership model) is retained, but the admin/user distinction
// has NO PRACTICAL EFFECT: every authenticated user is always treated as
// having full admin authority.
//
// This file covers the pkg/config half of that behavior:
//   - normalizeAdminOnlyRoles: in-memory role normalization, called from
//     loadConfigInternal on every config.LoadConfig / LoadConfigWithStore.
//   - selfHealUserRolesOnDisk: the on-disk self-heal write-back, so
//     config.json can never durably disagree with runtime behavior.
//
// Traces to: operator directive "we have now a one user instance there are
// no admin and normal users anymore ... remove the admin only logic from the
// entire system" (2026-07).

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadConfig_NormalizesUserRoleToAdmin_OnDiskSelfHeal proves BOTH halves
// of the single-user model's load-time fix in the SAME assertion: (a) a
// Gateway.Users entry persisted with role="user" is normalized to
// UserRoleAdmin in the returned *Config, AND (b) the on-disk config.json is
// rewritten in the SAME load pass so a subsequent independent read of the
// raw file also shows "admin" — config.json can never durably disagree with
// runtime behavior.
func TestLoadConfig_NormalizesUserRoleToAdmin_OnDiskSelfHeal(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "alice", "password_hash": "hash1", "token_hash": "", "role": "admin"},
				{"username": "bob", "password_hash": "hash2", "token_hash": "", "role": "user"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	// (a) In-memory: LoadConfig must return bob normalized to admin.
	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Gateway.Users, 2)

	byUsername := map[string]UserConfig{}
	for _, u := range cfg.Gateway.Users {
		byUsername[u.Username] = u
	}
	assert.Equal(t, UserRoleAdmin, byUsername["alice"].Role, "alice was already admin — must stay admin")
	assert.Equal(t, UserRoleAdmin, byUsername["bob"].Role,
		"single-user model: bob's role=user must be normalized to admin in memory")

	// (b) On-disk: the SAME load pass must have self-healed config.json so a
	// fresh, independent read of the raw JSON also shows "admin" for bob —
	// not just the in-memory struct. Read the raw bytes back (not via
	// LoadConfig again) to prove the FILE itself changed, not just that a
	// second load would re-normalize it.
	diskRaw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(diskRaw, &m))
	gw, ok := m["gateway"].(map[string]any)
	require.True(t, ok, "gateway section must exist on disk")
	users, ok := gw["users"].([]any)
	require.True(t, ok, "gateway.users must exist on disk")
	require.Len(t, users, 2)

	diskByUsername := map[string]map[string]any{}
	for _, u := range users {
		um, ok := u.(map[string]any)
		require.True(t, ok)
		diskByUsername[um["username"].(string)] = um
	}
	assert.Equal(t, "admin", diskByUsername["alice"]["role"])
	assert.Equal(t, "admin", diskByUsername["bob"]["role"],
		"single-user model: config.json on disk must be self-healed to role=admin in the SAME load pass")

	// Byte-level fidelity check: selfHealUserRolesOnDisk patches the raw JSON
	// map rather than round-tripping the full *Config struct via SaveConfig,
	// so fields carrying an explicit zero value under `omitempty` (like a
	// literal "token_hash":"") must survive the self-heal write, not vanish.
	_, hasTokenHash := diskByUsername["bob"]["token_hash"]
	assert.True(t, hasTokenHash, "self-heal must not drop unrelated omitempty-zero fields like token_hash")
	assert.Equal(t, "", diskByUsername["bob"]["token_hash"])
}

// TestLoadConfig_AllAdminRoles_NoOpNoRewrite proves normalizeAdminOnlyRoles
// is a true no-op (and, transitively, that selfHealUserRolesOnDisk never
// fires) when every Gateway.Users entry is already admin — the on-disk file
// must be byte-for-byte unchanged, proving LoadConfig does not perform a
// spurious rewrite on the common case (most configs already have only
// admins, or no users at all).
func TestLoadConfig_AllAdminRoles_NoOpNoRewrite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "alice", "password_hash": "hash1", "token_hash": "", "role": "admin"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	before, err := os.ReadFile(configPath)
	require.NoError(t, err)

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Gateway.Users, 1)
	assert.Equal(t, UserRoleAdmin, cfg.Gateway.Users[0].Role)

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"no self-heal write should occur when every user is already admin")
}

// TestNormalizeAdminOnlyRoles_MultipleNonAdminEntries proves the in-memory
// normalization handles more than one non-admin entry in a single pass and
// reports changed=true.
func TestNormalizeAdminOnlyRoles_MultipleNonAdminEntries(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			Users: []UserConfig{
				{Username: "alice", Role: UserRoleUser},
				{Username: "bob", Role: UserRoleAdmin},
				{Username: "carol", Role: UserRoleUser},
			},
		},
	}

	changed := normalizeAdminOnlyRoles(cfg)

	assert.True(t, changed)
	assert.Equal(t, UserRoleAdmin, cfg.Gateway.Users[0].Role)
	assert.Equal(t, UserRoleAdmin, cfg.Gateway.Users[1].Role)
	assert.Equal(t, UserRoleAdmin, cfg.Gateway.Users[2].Role)
}

// TestNormalizeAdminOnlyRoles_NoUsers_NoOp proves the function does not
// panic and reports changed=false when Gateway.Users is empty/nil (fresh
// install, no users configured yet).
func TestNormalizeAdminOnlyRoles_NoUsers_NoOp(t *testing.T) {
	cfg := &Config{}
	assert.False(t, normalizeAdminOnlyRoles(cfg))
}

// TestSelfHealUserRolesOnDisk_MissingGatewaySection_NoOp proves the on-disk
// self-heal degrades gracefully (returns nil bytes, nil error, does not
// panic) when config.json has no "gateway" section at all — e.g. a
// minimal/fresh config. The nil bytes signal "no write happened" to callers
// deciding whether to register a self-write hash (see SelfHealWriteHook).
func TestSelfHealUserRolesOnDisk_MissingGatewaySection_NoOp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"version":1}`), 0o600))

	written, err := selfHealUserRolesOnDisk(configPath)
	assert.NoError(t, err)
	assert.Nil(t, written, "no write occurred — written bytes must be nil")
}

// TestSelfHealUserRolesOnDisk_MissingFile_ReturnsError proves the self-heal
// surfaces a read error (wrapped) rather than silently succeeding when the
// target path does not exist — the loadConfigInternal call site logs this
// as a WARN and continues (in-memory normalization already makes runtime
// behavior correct), but the function itself must report the failure.
func TestSelfHealUserRolesOnDisk_MissingFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	written, err := selfHealUserRolesOnDisk(filepath.Join(dir, "does-not-exist.json"))
	assert.Error(t, err)
	assert.Nil(t, written)
}

// TestSelfHealUserRolesOnDisk_ReturnsWrittenBytes proves that when a write
// DOES happen, the function returns the exact bytes it wrote (not just nil
// error) — this is the contract LoadConfigWithStoreAndSelfHealHook relies on
// to report the write to its caller's onSelfHeal hook.
func TestSelfHealUserRolesOnDisk_ReturnsWrittenBytes(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{"version":1,"gateway":{"users":[{"username":"bob","role":"user"}]}}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	written, err := selfHealUserRolesOnDisk(configPath)
	require.NoError(t, err)
	require.NotNil(t, written, "a write occurred — written bytes must be non-nil")

	// The returned bytes must match what is actually on disk (byte-for-byte),
	// since the caller hashes `written` to register the app-initiated write.
	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	assert.Equal(t, string(onDisk), string(written))
}

// TestLoadConfigWithStoreAndSelfHealHook_InvokesHookOnWrite proves the hook
// threading mechanism (FIX1): loading a CurrentVersion config whose on-disk
// gateway.users[].role is "user" invokes onSelfHeal exactly once with the
// written bytes — this is what pkg/gateway's config-watcher polling loop and
// manual /reload trigger use to register the self-heal's write with their
// own selfWriteReg so it is not misidentified as a genuine external edit.
func TestLoadConfigWithStoreAndSelfHealHook_InvokesHookOnWrite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {"users": [{"username": "bob", "role": "user"}]}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	var hookCalls int
	var hookBytes []byte
	_, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func(writtenBytes []byte) {
		hookCalls++
		hookBytes = writtenBytes
	})
	require.NoError(t, err)

	assert.Equal(t, 1, hookCalls, "onSelfHeal must fire exactly once when a self-heal write occurs")
	require.NotNil(t, hookBytes)
	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	assert.Equal(t, string(onDisk), string(hookBytes),
		"the hook must receive the exact bytes written to disk, so the caller's hash registration matches")
}

// TestLoadConfigWithStoreAndSelfHealHook_NoWrite_HookNotInvoked proves the
// hook is never called when no self-heal write is needed (all roles already
// admin) — a nil/absent hook, or one that would panic on unexpected input,
// must be safe for the common case.
func TestLoadConfigWithStoreAndSelfHealHook_NoWrite_HookNotInvoked(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {"users": [{"username": "alice", "role": "admin"}]}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	hookCalled := false
	_, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func([]byte) {
		hookCalled = true
	})
	require.NoError(t, err)
	assert.False(t, hookCalled, "onSelfHeal must not fire when nothing needed healing")
}

// TestLoadConfigWithStore_NilHook_StillSelfHeals proves that the ordinary
// LoadConfig/LoadConfigWithStore entry points (nil hook, used by every
// caller that doesn't need write-dedup registration) still perform the
// on-disk self-heal correctly — passing a nil onSelfHeal must never skip or
// break the self-heal itself, only the notification of it.
func TestLoadConfigWithStore_NilHook_StillSelfHeals(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {"users": [{"username": "bob", "role": "user"}]}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfigWithStore(configPath, nil)
	require.NoError(t, err)
	require.Len(t, cfg.Gateway.Users, 1)
	assert.Equal(t, UserRoleAdmin, cfg.Gateway.Users[0].Role)

	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	var m map[string]any
	require.NoError(t, json.Unmarshal(onDisk, &m))
	gw := m["gateway"].(map[string]any)
	users := gw["users"].([]any)
	require.Len(t, users, 1)
	assert.Equal(t, "admin", users[0].(map[string]any)["role"],
		"on-disk role must still be self-healed even with a nil onSelfHeal hook")
}
