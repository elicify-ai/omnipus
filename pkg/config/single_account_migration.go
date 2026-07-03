// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package config — single-account invariant diagnostic.
//
// pkg/gateway/auth.go's checkBearerAuth and pkg/gateway/websocket.go's
// authenticateWS both check only Gateway.Users[0] (single-user model — the
// Users CRUD API that could ever mint a second account is deleted). A
// pre-single-user-model install could already have a second (or third...)
// account on disk from before this upgrade; left undiagnosed, every account
// past the first would silently and permanently fail bearer-token auth with
// no signal anywhere explaining why. warnAboutExtraUsers makes that gap
// loud instead of silent.
//
// Deliberately non-destructive: this does NOT truncate cfg.Gateway.Users or
// rewrite config.json. An earlier version of this fix truncated the extra
// entries (mirroring migrateCLITokenOutOfUsers's self-heal pattern), but
// that is unsafe here specifically — unlike the CLI-token relocation (which
// only ever touches a well-known, not-concurrently-logging-in "cli" row),
// LoadConfig is re-entered on every REST config write via
// safeUpdateConfigJSON → refreshConfigAndRewireServices (see
// pkg/gateway/rest.go), including HandleLogin's own token-append write.
// A destructive truncation would race a legitimate second account's
// in-flight login: user A logging in triggers a reload that truncates user
// B's still-real Gateway.Users row out from under them, before B ever gets
// a chance to log in. Logging only, with zero side effects, cannot race
// anything.
package config

import "github.com/dapicom-ai/omnipus/pkg/logger"

// warnAboutExtraUsers logs a WARN naming every Gateway.Users entry beyond
// the first when more than one is present, so an operator whose bearer
// token has started silently failing (because checkBearerAuth/
// authenticateWS only ever check Users[0]) has a diagnostic trail
// explaining why, instead of nothing.
//
// No-op when len(cfg.Gateway.Users) <= 1. Called on every config load —
// this is intentional (see package doc): it's a read-only diagnostic, not a
// one-shot migration, so re-warning on every load is a fine and simple
// trade-off in exchange for never risking data loss.
func warnAboutExtraUsers(cfg *Config) {
	if len(cfg.Gateway.Users) <= 1 {
		return
	}

	extra := make([]string, 0, len(cfg.Gateway.Users)-1)
	for _, u := range cfg.Gateway.Users[1:] {
		extra = append(extra, u.Username)
	}
	logger.WarnF("gateway.users has more than one account, but the single-user "+
		"model only ever authenticates the first entry — these accounts' bearer "+
		"tokens will fail on checkBearerAuth/authenticateWS with no other signal; "+
		"remove them from config.json (or promote the intended account to index 0) "+
		"to silence this warning", map[string]any{
		"kept":  cfg.Gateway.Users[0].Username,
		"extra": extra,
	})
}
