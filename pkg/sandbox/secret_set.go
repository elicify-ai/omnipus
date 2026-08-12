// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// SecretEntriesRelative is THE definition of what a sandboxed child must never
// reach inside $OMNIPUS_HOME. ADR-060 §4.0. It is deliberately the ONLY such
// list in the tree: the macOS backend renders it as denies and the Linux
// backend renders it as never-granted, and two lists would drift apart exactly
// where the drift is invisible (spec FR-2.3b).
//
// Entries are one level deep, and that is a property of the install layout
// rather than a simplification — VERIFIED against a real $OMNIPUS_HOME. The
// exclusion therefore needs no recursive walk, which is what keeps the Linux
// sibling-granting in FR-4.5 cheap enough to run per spawn.
//
// # Why each entry is here
//
//   - master.key       — the key every stored credential is encrypted under.
//     Truncating it destroys the vault irreversibly, with no read involved,
//     which is why write matters as much as read here.
//   - credentials.json — the vault itself.
//   - config.json      — defines the sandbox. A child that can write it can set
//     sandbox.mode: off and remove its own confinement on the next boot.
//   - cli.token        — a LIVE gateway bearer token. Reading it is full API
//     access as the operator, from inside the sandbox.
//   - entities         — per-agent tool policy. A child that can write it can
//     flip its own tools to allow.
//
// # Why agents/ is NOT here, though it looks like it belongs
//
// `agents/` holds agent WORKSPACES — the directories agents legitimately work
// in — while `entities/` holds their POLICY. An earlier draft of this list said
// `agents/`, which would have made every agent's own working directory
// unwritable and broken the product outright while appearing to harden it. The
// two are distinct trees on disk; only policy is excluded.
var SecretEntriesRelative = []string{
	"master.key",
	"credentials.json",
	"config.json",
	"cli.token",
	"entities",
}

// secretGlobPrefixes are basename PREFIXES whose every match is a secret,
// alongside the exact names in SecretEntriesRelative.
//
// This exists because protecting config.json alone was not enough. A real
// install carries files like `config.json.bak-20260811-224607`, written when a
// migration rewrites the config, and a backup holds exactly what the original
// holds: VERIFIED on a live install, the backup contained both the gateway CLI
// token and the user account list. Denying the original while leaving a
// byte-identical copy beside it readable is a deny that looks correct and
// protects nothing.
//
// Prefix matching rather than an exact list because the suffix is a timestamp:
// the set is open-ended by construction, so enumerating it would go stale the
// next time a migration runs — the same defect ADR-060 exists to remove.
var secretGlobPrefixes = []string{
	"config.json.",      // config.json.bak-<timestamp>, and any future suffix
	"credentials.json.", // same shape, same reasoning
	"master.key.",
}

// SecretPaths returns the absolute path of every secret under homePath: the
// exact SecretEntriesRelative names first, then any existing file whose
// basename starts with a secretGlobPrefixes entry. Returns nil for an empty
// homePath so a caller with no configured home does not produce denies rooted
// at "/".
//
// The exact names are returned whether or not they exist. A credential file
// created after boot must already be covered (spec FR-3.4); returning only
// extant paths would leave a window in which the file is created and readable.
//
// The prefix matches are necessarily discovered by listing, so they cover what
// is on disk at the time of the call. On Linux that call happens per spawn, so
// a backup written mid-session is covered by the next child. On macOS the
// profile is rendered once at boot — a backup written afterwards is NOT denied
// until restart, which is a real and accepted residual: config backups are
// written by migrations, which run at boot before the profile is rendered.
func SecretPaths(homePath string) []string {
	if homePath == "" {
		return nil
	}
	home := filepath.Clean(homePath)
	out := make([]string, 0, len(SecretEntriesRelative)+4)
	seen := make(map[string]struct{}, len(SecretEntriesRelative)+4)
	for _, name := range SecretEntriesRelative {
		p := filepath.Join(home, name)
		out = append(out, p)
		seen[p] = struct{}{}
	}

	// Listing failure is deliberately NOT fatal here: the exact names above are
	// the load-bearing protection and are already in the slice. The Linux
	// caller (ExpandRulesExcluding) fails the spawn on its own listing errors,
	// so a genuinely unreadable home still fails closed there.
	entries, err := os.ReadDir(home)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		for _, prefix := range secretGlobPrefixes {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			p := filepath.Join(home, name)
			if _, dup := seen[p]; dup {
				break
			}
			seen[p] = struct{}{}
			out = append(out, p)
			break
		}
	}
	return out
}

// IsSecretEntry reports whether a $OMNIPUS_HOME-relative entry name is in the
// secret set. Used by the Linux sibling-granting walk, which decides per
// directory entry whether to grant it.
func IsSecretEntry(name string) bool {
	for _, prefix := range secretGlobPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, s := range SecretEntriesRelative {
		if s == name {
			return true
		}
	}
	return false
}
