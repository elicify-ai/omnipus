// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package config — CLI-token relocation migration.
//
// migrateCLITokenOutOfUsers is a one-shot, best-effort relocation: legacy
// config.json files minted a role-less "cli" entry in Gateway.Users purely
// to hold the CLI's bearer-token hash (a role-less Users[] row was the only
// place a machine credential could live before Gateway.CLIToken existed).
// Now that the human-account/role concept is gone, that token gets its own
// dedicated Gateway.CLIToken slot instead of masquerading as a "user".
//
// This mirrors the retired selfHealUserRolesOnDisk's raw-JSON-map
// read/patch/write technique (config.go's git history) so the on-disk file
// is patched byte-for-byte rather than round-tripped through SaveConfig,
// which silently drops any field carrying an explicit zero value under an
// `omitempty` JSON tag.
package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// legacyCLIUsername is the role-less Gateway.Users[].Username value legacy
// config.json files used to hold the CLI's bearer-token hash.
const legacyCLIUsername = "cli"

// migrateCLITokenOutOfUsers finds and pops a Gateway.Users entry named "cli"
// and relocates its token material into cfg.Gateway.CLIToken, then persists
// the same change to cfgPath on disk. Best-effort by design (unlike the
// retired selfHealUserRolesOnDisk, callers never fail the load over this):
// on any write failure it logs a warning and leaves the legacy shape in
// place on disk (the in-memory cfg is still corrected, so runtime behavior
// is correct regardless of whether the on-disk self-heal succeeded).
//
// Idempotent no-op when cfg.Gateway.CLIToken is already set (already
// migrated) or when no "cli"-named entry exists in cfg.Gateway.Users.
//
// When onSelfHeal is non-nil and the on-disk patch performs a write, it is
// invoked with the exact bytes written (see SelfHealWriteHook's doc comment
// on config.go for why this is threaded through rather than pkg/config
// writing silently).
func migrateCLITokenOutOfUsers(cfg *Config, cfgPath string, onSelfHeal SelfHealWriteHook) {
	if cfg.Gateway.CLIToken != nil {
		// Already migrated (or a fresh config that never had a "cli" row).
		return
	}

	idx := -1
	for i := range cfg.Gateway.Users {
		if cfg.Gateway.Users[i].Username == legacyCLIUsername {
			idx = i
			break
		}
	}
	if idx == -1 {
		// No legacy "cli" entry to relocate — nothing to do.
		return
	}

	legacy := cfg.Gateway.Users[idx]

	var entry *TokenEntry
	switch {
	case len(legacy.Tokens) > 0:
		// Preserve the most recently issued token entry (ID/Hash/CreatedAt) as-is.
		last := legacy.Tokens[len(legacy.Tokens)-1]
		entry = &last
	case !legacy.TokenHash.IsZero():
		entry = &TokenEntry{Hash: legacy.TokenHash}
	}

	logger.InfoF("relocating legacy \"cli\" Gateway.Users entry to Gateway.CLIToken "+
		"(single-user model: machine credentials no longer live in Users[])", map[string]any{
		"had_token": entry != nil,
	})

	cfg.Gateway.CLIToken = entry
	cfg.Gateway.Users = append(cfg.Gateway.Users[:idx], cfg.Gateway.Users[idx+1:]...)

	written, healErr := migrateCLITokenOnDisk(cfgPath)
	if healErr != nil {
		logger.WarnF("failed to persist CLI-token relocation to config.json; "+
			"runtime behavior is still correct (in-memory state is migrated), "+
			"but the on-disk file could not be self-healed", map[string]any{
			"path":  cfgPath,
			"error": healErr.Error(),
		})
		return
	}
	if written != nil && onSelfHeal != nil {
		onSelfHeal(written)
	}
}

// migrateCLITokenOnDisk rewrites config.json at path to move a legacy
// gateway.users[] entry named "cli" into a new gateway.cli_token field,
// preserving every other field/byte of structure untouched (raw-JSON-map
// read/patch/write — see the package doc comment).
//
// Returns (nil, nil) — a true no-op, no write performed — when: the file has
// no gateway section; gateway.cli_token is already present on disk (already
// migrated); or there is no "cli"-named entry in gateway.users. On a
// successful write, returns the exact bytes written (never nil).
func migrateCLITokenOnDisk(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config for CLI-token migration: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return nil, fmt.Errorf("parse config for CLI-token migration: %w", unmarshalErr)
	}
	gw, ok := m["gateway"].(map[string]any)
	if !ok {
		return nil, nil // no gateway section on disk — nothing to migrate
	}
	if _, hasCLIToken := gw["cli_token"]; hasCLIToken {
		return nil, nil // already migrated on disk
	}
	usersRaw, ok := gw["users"].([]any)
	if !ok {
		return nil, nil // no users on disk — nothing to migrate
	}

	idx := -1
	var cliUser map[string]any
	for i, u := range usersRaw {
		um, ok := u.(map[string]any)
		if !ok {
			continue
		}
		if username, _ := um["username"].(string); username == legacyCLIUsername {
			idx = i
			cliUser = um
			break
		}
	}
	if idx == -1 {
		return nil, nil // no legacy "cli" entry on disk — nothing to migrate
	}

	// Build the cli_token entry from whatever token material the legacy row
	// carries: prefer the newest entry of the token SET, falling back to the
	// legacy single token_hash field.
	var tokenEntry map[string]any
	if tokens, ok := cliUser["tokens"].([]any); ok && len(tokens) > 0 {
		if last, ok := tokens[len(tokens)-1].(map[string]any); ok {
			tokenEntry = last
		}
	}
	if tokenEntry == nil {
		if hash, ok := cliUser["token_hash"].(string); ok && hash != "" {
			tokenEntry = map[string]any{"hash": hash}
		}
	}
	if tokenEntry != nil {
		gw["cli_token"] = tokenEntry
	}

	// Drop the legacy "cli" row from gateway.users.
	newUsers := make([]any, 0, len(usersRaw)-1)
	newUsers = append(newUsers, usersRaw[:idx]...)
	newUsers = append(newUsers, usersRaw[idx+1:]...)
	gw["users"] = newUsers

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialize config for CLI-token migration: %w", err)
	}
	if writeErr := fileutil.WriteFileAtomic(path, out, 0o600); writeErr != nil {
		return nil, fmt.Errorf("write config for CLI-token migration: %w", writeErr)
	}
	return out, nil
}
