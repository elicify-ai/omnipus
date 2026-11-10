// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// ADR-094 FR-015 (founder decision 2026-11-09): gateway.users[].
// session_issued_at round-trips through config.json — load, save, load — so
// the planted-cookie guard can rebuild the genuine cookie's original expiry.
// An older user record without the key loads as empty (never re-sent).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserConfig_SessionIssuedAt_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	const issued = "2026-11-09T06:00:00Z"

	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"gateway": {
			"users": [
				{"username": "alice", "session_token_hash": "bcrypthash-alice", "session_issued_at": "` + issued + `"},
				{"username": "bob", "session_token_hash": "bcrypthash-bob"}
			]
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Gateway.Users, 2)
	require.Equal(t, issued, cfg.Gateway.Users[0].SessionIssuedAt, "load reads session_issued_at")
	require.Empty(t, cfg.Gateway.Users[1].SessionIssuedAt, "a record without the key loads as empty")

	require.NoError(t, SaveConfig(configPath, cfg))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	gw, ok := m["gateway"].(map[string]any)
	require.True(t, ok, "gateway is an object")
	users, ok := gw["users"].([]any)
	require.True(t, ok, "gateway.users is an array")
	require.Len(t, users, 2)
	alice, ok := users[0].(map[string]any)
	require.True(t, ok)
	bob, ok := users[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, issued, alice["session_issued_at"], "save writes session_issued_at")
	_, present := bob["session_issued_at"]
	require.False(t, present, "an empty issue time is omitted on save, not written as \"\"")

	again, err := LoadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, issued, again.Gateway.Users[0].SessionIssuedAt, "the value survives load-save-load")
}
