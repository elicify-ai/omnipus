// cli_token_migration_test.go — CLI-token relocation migration tests.
//
// Covers migrateCLITokenOutOfUsers / migrateCLITokenOnDisk: relocating a
// legacy role-less "cli" Gateway.Users entry into the dedicated
// Gateway.CLIToken slot, on load, exactly once, on-disk and in-memory.
//
// Traces to the CLI-token relocation companion of the single-user model
// (operator directive, 2026-07: remove the admin/user role concept —
// Gateway.CLIToken replaces the role-less "cli" Users[] row that used to
// exist only to hold the CLI's bearer-token hash).

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadConfig_RelocatesLegacyCLIUser_InMemory proves (a): a config.json
// with a legacy Gateway.Users entry named "cli" (holding a TokenHash) and no
// Gateway.CLIToken set, after LoadConfig, ends up with cfg.Gateway.CLIToken
// populated from that entry's hash and no "cli"-named entry left in Users.
func TestLoadConfig_RelocatesLegacyCLIUser_InMemory(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "cli", "token_hash": "$2a$10$legacyclihash"},
				{"username": "alice", "session_token_hash": "bcrypthash"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)

	require.NotNil(t, cfg.Gateway.CLIToken, "CLIToken must be populated from the legacy \"cli\" entry")
	assert.Equal(t, BcryptHash("$2a$10$legacyclihash"), cfg.Gateway.CLIToken.Hash)

	for _, u := range cfg.Gateway.Users {
		assert.NotEqual(t, legacyCLIUsername, u.Username,
			"the \"cli\" entry must be removed from Gateway.Users after relocation")
	}
	require.Len(t, cfg.Gateway.Users, 1, "only the real human account remains")
	assert.Equal(t, "alice", cfg.Gateway.Users[0].Username)
}

// TestLoadConfig_RelocatesLegacyCLIUser_PreservesTokenSetEntry proves the
// migration preserves a full TokenEntry (ID/Hash/CreatedAt) — not just a
// bare hash — when the legacy "cli" row carries the newer Tokens set rather
// than the single legacy TokenHash field, and picks the most recently
// issued entry when more than one is present.
func TestLoadConfig_RelocatesLegacyCLIUser_PreservesTokenSetEntry(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "cli", "tokens": [
					{"id": "aaa111", "hash": "$2a$10$oldtoken", "created_at": "2026-01-01T00:00:00Z"},
					{"id": "bbb222", "hash": "$2a$10$newesttoken", "created_at": "2026-06-01T00:00:00Z"}
				]}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)

	require.NotNil(t, cfg.Gateway.CLIToken)
	assert.Equal(t, "bbb222", cfg.Gateway.CLIToken.ID, "must pick the newest (last) token entry")
	assert.Equal(t, BcryptHash("$2a$10$newesttoken"), cfg.Gateway.CLIToken.Hash)
	assert.Equal(t, "2026-06-01T00:00:00Z", cfg.Gateway.CLIToken.CreatedAt)
}

// TestLoadConfig_RelocatesLegacyCLIUser_OnDisk proves (b): the on-disk
// config.json file reflects the same relocation after the migration write —
// gateway.cli_token is present and gateway.users no longer carries a "cli"
// entry, checked via an independent raw-JSON read (not another LoadConfig
// call) so the assertion proves the FILE changed, not just the in-memory
// struct.
func TestLoadConfig_RelocatesLegacyCLIUser_OnDisk(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "cli", "token_hash": "$2a$10$legacyclihash"},
				{"username": "alice", "session_token_hash": "bcrypthash"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	_, err := LoadConfig(configPath)
	require.NoError(t, err)

	diskRaw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(diskRaw, &m))

	gw, ok := m["gateway"].(map[string]any)
	require.True(t, ok, "gateway section must exist on disk")

	cliToken, ok := gw["cli_token"].(map[string]any)
	require.True(t, ok, "gateway.cli_token must be present on disk after relocation")
	assert.Equal(t, "$2a$10$legacyclihash", cliToken["hash"])

	users, ok := gw["users"].([]any)
	require.True(t, ok)
	require.Len(t, users, 1, "the \"cli\" row must be dropped from gateway.users on disk")
	um, ok := users[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "alice", um["username"])
}

// TestLoadConfig_NoCLIUser_NoOpNoRewrite proves (c): a config with no "cli"
// entry at all is a true no-op — the on-disk file must be byte-for-byte
// unchanged (no spurious rewrite), and CLIToken stays nil.
func TestLoadConfig_NoCLIUser_NoOpNoRewrite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "alice", "session_token_hash": "bcrypthash"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	before, err := os.ReadFile(configPath)
	require.NoError(t, err)

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	assert.Nil(t, cfg.Gateway.CLIToken)
	require.Len(t, cfg.Gateway.Users, 1)
	assert.Equal(t, "alice", cfg.Gateway.Users[0].Username)

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"no migration write should occur when there is no legacy \"cli\" entry")
}

// TestLoadConfig_AlreadyMigratedShape_NoOpNoRewrite proves (c): a config
// already in the new shape (gateway.cli_token already set, no "cli" entry in
// users) is a no-op — no spurious rewrite, and the existing CLIToken value
// is preserved untouched.
func TestLoadConfig_AlreadyMigratedShape_NoOpNoRewrite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"cli_token": {"id": "zzz999", "hash": "$2a$10$currenttoken", "created_at": "2026-07-01T00:00:00Z"},
			"users": [
				{"username": "alice", "session_token_hash": "bcrypthash"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	before, err := os.ReadFile(configPath)
	require.NoError(t, err)

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg.Gateway.CLIToken)
	assert.Equal(t, "zzz999", cfg.Gateway.CLIToken.ID)
	assert.Equal(t, BcryptHash("$2a$10$currenttoken"), cfg.Gateway.CLIToken.Hash)

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"no migration write should occur when the config is already in the new shape")
}

// TestMigrateCLITokenOnDisk_MissingGatewaySection_NoOp proves the on-disk
// migration degrades gracefully (returns nil bytes, nil error, does not
// panic) when config.json has no "gateway" section at all.
func TestMigrateCLITokenOnDisk_MissingGatewaySection_NoOp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"version":1}`), 0o600))

	written, err := migrateCLITokenOnDisk(configPath)
	assert.NoError(t, err)
	assert.Nil(t, written, "no write occurred — written bytes must be nil")
}

// TestMigrateCLITokenOnDisk_MissingFile_ReturnsError proves the migration
// surfaces a read error (wrapped) rather than silently succeeding when the
// target path does not exist — migrateCLITokenOutOfUsers logs this as a WARN
// and continues (in-memory relocation already makes runtime behavior
// correct), but the function itself must report the failure.
func TestMigrateCLITokenOnDisk_MissingFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	written, err := migrateCLITokenOnDisk(filepath.Join(dir, "does-not-exist.json"))
	assert.Error(t, err)
	assert.Nil(t, written)
}

// TestLoadConfigWithStoreAndSelfHealHook_InvokesHookOnCLITokenMigration
// proves the SelfHealWriteHook mechanism (originally built for the retired
// role self-heal) is still wired up correctly for its current writer: loading
// a CurrentVersion config with a legacy "cli" Users entry invokes onSelfHeal
// exactly once with the bytes written to disk — this is what pkg/gateway's
// config-watcher polling loop and manual /reload trigger use to register the
// self-heal's write with their own selfWriteReg so it is not misidentified
// as a genuine external edit.
func TestLoadConfigWithStoreAndSelfHealHook_InvokesHookOnCLITokenMigration(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {"users": [{"username": "cli", "token_hash": "$2a$10$legacyclihash"}]}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	var hookCalls int
	var hookBytes []byte
	_, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func(writtenBytes []byte) {
		hookCalls++
		hookBytes = writtenBytes
	})
	require.NoError(t, err)

	assert.Equal(t, 1, hookCalls, "onSelfHeal must fire exactly once when a CLI-token migration write occurs")
	require.NotNil(t, hookBytes)
	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	assert.Equal(t, string(onDisk), string(hookBytes),
		"the hook must receive the exact bytes written to disk, so the caller's hash registration matches")
}

// TestLoadConfigWithStoreAndSelfHealHook_NoCLIMigration_HookNotInvoked
// proves the hook is never called when no CLI-token migration write is
// needed (no legacy "cli" entry present).
func TestLoadConfigWithStoreAndSelfHealHook_NoCLIMigration_HookNotInvoked(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {"users": [{"username": "alice", "session_token_hash": "bcrypthash"}]}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	hookCalled := false
	_, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func([]byte) {
		hookCalled = true
	})
	require.NoError(t, err)
	assert.False(t, hookCalled, "onSelfHeal must not fire when nothing needed migrating")
}

// TestVerifyTokenAgainst_MatchesCLITokenSlot proves the extracted
// VerifyTokenAgainst helper works directly against a Gateway.CLIToken slot
// (a single *TokenEntry, not a UserConfig) — the shape pkg/gateway's
// auth/websocket CLI-token check is expected to use.
func TestVerifyTokenAgainst_MatchesCLITokenSlot(t *testing.T) {
	hash := hashFull(t, "supersecretclitoken")

	cliToken := TokenEntry{ID: "cli01", Hash: hash}

	err := VerifyTokenAgainst([]TokenEntry{cliToken}, BcryptHash(""), "supersecretclitoken")
	assert.NoError(t, err, "correct raw token must verify against the CLIToken slot")

	err = VerifyTokenAgainst([]TokenEntry{cliToken}, BcryptHash(""), "wrong-token")
	assert.Error(t, err, "incorrect raw token must fail verification")

	err = VerifyTokenAgainst(nil, BcryptHash(""), "supersecretclitoken")
	assert.ErrorIs(t, err, ErrNoHashSet, "an empty token set must report ErrNoHashSet")
}
