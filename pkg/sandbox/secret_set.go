// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import "path/filepath"

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

// SecretPaths returns the absolute path of every SecretEntriesRelative entry
// under homePath, in list order. Returns nil for an empty homePath so a caller
// with no configured home does not produce denies rooted at "/".
//
// Paths are returned whether or not they exist. A credential file created after
// boot must already be covered (spec FR-3.4); returning only extant paths would
// leave a window in which the file is created and readable.
func SecretPaths(homePath string) []string {
	if homePath == "" {
		return nil
	}
	home := filepath.Clean(homePath)
	out := make([]string, 0, len(SecretEntriesRelative))
	for _, name := range SecretEntriesRelative {
		out = append(out, filepath.Join(home, name))
	}
	return out
}

// IsSecretEntry reports whether a $OMNIPUS_HOME-relative entry name is in the
// secret set. Used by the Linux sibling-granting walk, which decides per
// directory entry whether to grant it.
func IsSecretEntry(name string) bool {
	for _, s := range SecretEntriesRelative {
		if s == name {
			return true
		}
	}
	return false
}
