// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package config — single-account invariant diagnostic.
//
// pkg/gateway/auth.go's checkBearerAuth, pkg/gateway/websocket.go's
// authenticateWS, and pkg/gateway/rest_auth.go's withOptionalAuth all LOOP
// over every entry in Gateway.Users, so every configured account
// authenticates normally today — nothing about bearer-token auth is broken.
// This file exists only because the single-user model expects exactly one
// account, and a couple of NON-auth code paths still only ever consider the
// first entry: the default workspace-owner attribution
// (pkg/gateway/gateway.go's Gateway.Users[0] usage) and the
// schedule-failure-notification fallback (pkg/gateway/schedules.go's
// Gateway.Users[0] usage). A pre-single-user-model install could still have
// a second (or third...) account on disk from before the Users CRUD API was
// deleted; warnAboutExtraUsers surfaces that mismatch as an advisory WARN so
// an operator can consolidate to one account if the extras are
// unintentional leftovers — it does not claim anything is currently broken.
//
// Deliberately non-destructive: this does NOT truncate Gateway.Users or
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

// warnAboutExtraUsers logs an advisory WARN naming every Gateway.Users entry
// beyond the first when more than one is present. This is NOT a
// "your tokens will fail" alarm — checkBearerAuth, authenticateWS, and
// withOptionalAuth all loop the whole slice, so every one of these accounts
// authenticates fine. The WARN exists because a couple of non-auth code
// paths (default workspace-owner attribution, schedule-failure-notification
// fallback — see the Gateway.Users[0] usages in pkg/gateway/gateway.go and
// pkg/gateway/schedules.go) still only ever consider the first entry, and
// the single-user model expects exactly one account. It invites the
// operator to consolidate to one account if the extras are unintentional
// leftovers from before the Users CRUD API was deleted — it is not an
// instruction to manually delete a still-working account.
//
// No-op when len(users) <= 1. Called on every config load — this is
// intentional (see package doc): it's a read-only diagnostic, not a
// one-shot migration, so re-warning on every load is a fine and simple
// trade-off in exchange for never risking data loss.
func warnAboutExtraUsers(users []UserConfig) {
	if len(users) <= 1 {
		return
	}

	extra := make([]string, 0, len(users)-1)
	for _, u := range users[1:] {
		extra = append(extra, u.Username)
	}
	logger.WarnF("gateway.users has more than one account; all of them authenticate "+
		"normally, but the single-user model expects exactly one, and the default "+
		"workspace-owner attribution plus the schedule-failure-notification fallback "+
		"only ever consider the first entry — consolidate to one account (or promote "+
		"the intended account to index 0) if these extras are unintentional leftovers, "+
		"to silence this warning", map[string]any{
		"kept":  users[0].Username,
		"extra": extra,
	})
}
