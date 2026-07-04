//go:build !cgo

// single_account_migration_test.go — Gateway.Users diagnostic tests.
//
// Covers warnAboutExtraUsers: a deliberately non-destructive diagnostic (see
// single_account_migration.go's package doc for why a truncating self-heal
// is unsafe here specifically — it would race a legitimate second account's
// own in-flight login, since LoadConfig is re-entered on every REST config
// write via safeUpdateConfigJSON).
//
// Traces to the single-account-diagnostic companion of the single-user
// model (operator directive, 2026-07: remove the admin/user role concept —
// checkBearerAuth/authenticateWS/withOptionalAuth loop over Gateway.Users,
// so every configured account still authenticates; this is a pure
// diagnostic that a legacy config has more than the one account the
// single-user model expects, nothing more).

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadConfig_ExtraUsers_NotTruncated_InMemory proves (a): a config.json
// with 2+ Gateway.Users entries, after LoadConfig, still has ALL entries in
// cfg.Gateway.Users — no truncation, no data loss.
func TestLoadConfig_ExtraUsers_NotTruncated_InMemory(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "alice", "session_token_hash": "bcrypthash-alice"},
				{"username": "bob", "session_token_hash": "bcrypthash-bob"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)

	require.Len(t, cfg.Gateway.Users, 2, "both accounts must survive — no truncation")
	assert.Equal(t, "alice", cfg.Gateway.Users[0].Username)
	assert.Equal(t, "bob", cfg.Gateway.Users[1].Username)
}

// TestLoadConfig_ExtraUsers_NotTruncated_OnDisk proves (b): config.json on
// disk is completely untouched by the diagnostic — no write at all.
func TestLoadConfig_ExtraUsers_NotTruncated_OnDisk(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "alice", "session_token_hash": "bcrypthash-alice"},
				{"username": "bob", "session_token_hash": "bcrypthash-bob"},
				{"username": "carol", "session_token_hash": "bcrypthash-carol"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	_, err := LoadConfig(configPath)
	require.NoError(t, err)

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(after, &m))
	gw, ok := m["gateway"].(map[string]any)
	require.True(t, ok)
	users, ok := gw["users"].([]any)
	require.True(t, ok)
	require.Len(t, users, 3, "on-disk users array must be completely untouched — 3 entries, no truncation")
}

// TestLoadConfig_SingleUser_NoOp proves (c): the common case (0 or 1
// Gateway.Users entries) triggers no diagnostic behavior and is
// unaffected.
func TestLoadConfig_SingleUser_NoOp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "alice", "session_token_hash": "bcrypthash-alice"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Gateway.Users, 1)
	assert.Equal(t, "alice", cfg.Gateway.Users[0].Username)
}

// TestLoadConfig_ZeroUsers_NoOp proves (d): a config with no Gateway.Users
// at all is unaffected (no panic).
func TestLoadConfig_ZeroUsers_NoOp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": []
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	assert.Empty(t, cfg.Gateway.Users)
}

// TestLoadConfig_CLITokenRelocation_ExtraHumanAccountsUntouched proves the
// interaction between the two Gateway.Users-adjacent load-time behaviors: a
// legacy config with a role-less "cli" row (relocated to Gateway.CLIToken
// by migrateCLITokenOutOfUsers) AND multiple genuine human accounts ends up
// with the "cli" row gone (relocated) and ALL human accounts still present
// — warnAboutExtraUsers only logs, it does not remove alice or bob.
func TestLoadConfig_CLITokenRelocation_ExtraHumanAccountsUntouched(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "alice", "session_token_hash": "bcrypthash-alice"},
				{"username": "bob", "session_token_hash": "bcrypthash-bob"},
				{"username": "cli", "token_hash": "$2a$10$legacyclihash"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)

	require.NotNil(t, cfg.Gateway.CLIToken, "the legacy \"cli\" entry must still be relocated")
	assert.Equal(t, BcryptHash("$2a$10$legacyclihash"), cfg.Gateway.CLIToken.Hash)

	require.Len(
		t,
		cfg.Gateway.Users,
		2,
		"both human accounts must survive — only \"cli\" is relocated, nothing is truncated",
	)
	assert.Equal(t, "alice", cfg.Gateway.Users[0].Username)
	assert.Equal(t, "bob", cfg.Gateway.Users[1].Username)
}
