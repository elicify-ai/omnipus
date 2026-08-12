// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"path/filepath"
	"strings"
)

// SecretEntriesRelative is THE definition of what must never be reachable
// inside $OMNIPUS_HOME. ADR-061 D3, spec FR-3.1/FR-3.2. It is the union of two
// lists that used to live apart, and each half protected something the other
// missed:
//
//	master.key, credentials.json  — both lists had these; root of trust
//	config.json, cli.token        — kernel only. config.json lets a child turn
//	                                its own sandbox off on the next boot;
//	                                cli.token is a LIVE gateway bearer token
//	entities                      — both; per-agent tool policy
//	agents, workspaces            — app layer only; cross-agent isolation
//
// # Why this lives in pkg/fspolicy and not pkg/sandbox
//
// It is data about the $OMNIPUS_HOME layout, so the leaf package is where it
// belongs on merit. It is also the only place it CAN live: pkg/tools imports
// pkg/sandbox, and ADR-046's P3 wires pkg/sandbox -> pkg/fspolicy, so a
// definition in pkg/sandbox that fspolicy had to import would be an import
// cycle. An earlier draft of the spec specified exactly that and would not have
// compiled. pkg/sandbox.SecretEntriesRelative is an alias for this.
//
// # It is split, and the split is load-bearing
//
// SecretEntriesAlways needs no context. SecretEntriesPerTurn (agents/,
// workspaces/) only means anything once a work dir is known, because those
// roots contain the caller's OWN directory and denying them outright locks an
// agent out of the place it is meant to work. Use SecretPathsAlways where there
// is no turn (kernel boot) and DeniedPathsFor where there is. This combined
// list exists for the app layer, which always has a work dir, and for callers
// that genuinely want the whole vocabulary.
var SecretEntriesRelative = append(append([]string{}, SecretEntriesAlways...), SecretEntriesPerTurn...)

// SecretEntriesAlways is the part of the set that needs NO turn context: no
// agent, in any shape, ever has a legitimate reason to reach these.
//
// This is the subset the KERNEL BOOT policy can use directly, because at boot
// there is no work dir to compare against.
var SecretEntriesAlways = []string{
	"master.key",
	"credentials.json",
	"config.json",
	"cli.token",
	"entities",
}

// SecretEntriesPerTurn is the part that is only meaningful WITH a work dir,
// because of the own-tree exception.
//
// `agents/` and `workspaces/` are COARSE roots: they cover every agent's home
// and every workspace, including the caller's own. Denying them outright would
// lock an agent out of the directory it is supposed to be working in — the
// exclusion would break the product while looking like hardening.
//
// So these are only excluded once a work dir is known, via DeniedPathsFor,
// which re-admits the caller's own tree under exactly the same per-root rule
// IsCarveOut applies. Using them without a work dir is a bug; that is why they
// are a separate list rather than a comment on a combined one.
var SecretEntriesPerTurn = []string{
	"agents",
	"workspaces",
}

// secretGlobPrefixes are basename PREFIXES whose every match is a secret.
//
// Protecting config.json alone was not enough: a real install carries
// `config.json.bak-<timestamp>`, written when a migration rewrites the config,
// and VERIFIED on a live install that backup held both the gateway CLI token
// and the user account list. Denying the original while a byte-identical copy
// sits readable beside it is a deny that reads as correct and protects nothing.
//
// Prefix rather than an exact list because the suffix is a timestamp: the set
// is open-ended by construction, so enumerating it would go stale the next time
// a migration runs.
var secretGlobPrefixes = []string{
	"config.json.",
	"credentials.json.",
	"master.key.",
}

// SecretPaths returns the absolute path of every secret under home: the exact
// SecretEntriesRelative names, then any existing entry whose basename starts
// with a secretGlobPrefixes entry.
//
// The exact names are returned whether or not they exist — a credential file
// created after this call must already be covered, and returning only extant
// paths would leave a window in which it is created and readable. The prefix
// matches are necessarily discovered by listing, so they cover what is on disk
// at call time.
//
// Returns nil for an empty home so a caller with no configured home does not
// produce denies rooted at "/".
func SecretPaths(home string) []string {
	if home == "" {
		return nil
	}
	clean := filepath.Clean(home)
	out := make([]string, 0, len(SecretEntriesRelative)+4)
	seen := make(map[string]struct{}, len(SecretEntriesRelative)+4)
	for _, name := range SecretEntriesRelative {
		p := filepath.Join(clean, name)
		out = append(out, p)
		seen[p] = struct{}{}
	}
	for _, p := range secretBackupPaths(clean) {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// SecretPathsAlways returns only the context-free part of the set: the entries
// no agent may reach in any shape. Use this where there is no work dir to
// compare against — the kernel policy computed at boot, before any turn exists.
//
// Using SecretPaths there instead would deny `agents/` and `workspaces/`
// wholesale to every child, locking every agent out of its own working
// directory. That is not a hypothetical: it is what the first cut of this
// change did, and four tests caught it immediately.
func SecretPathsAlways(home string) []string {
	if home == "" {
		return nil
	}
	clean := filepath.Clean(home)
	out := make([]string, 0, len(SecretEntriesAlways)+4)
	for _, name := range SecretEntriesAlways {
		out = append(out, filepath.Join(clean, name))
	}
	return append(out, secretBackupPaths(clean)...)
}

// DeniedPathsFor returns the paths a turn with this work dir must not reach:
// every SecretEntriesAlways entry, plus the SecretEntriesPerTurn roots MINUS
// the one containing the caller's own work dir.
//
// The exception is deliberately the same per-root rule IsCarveOut applies, and
// it is narrow: a root R is re-admitted only when workDir is a PROPER
// DESCENDANT of R. Two consequences that are easy to get backwards, and are
// both intended:
//
//   - Agent-home-rooted turn (workDir == <home>/agents/<self>): `agents` is
//     re-admitted, so the agent reaches its own home. It still cannot reach
//     ANOTHER agent's home, because the app layer's IsCarveOut applies the same
//     rule at path granularity.
//   - Re-rooted workspace turn (workDir == <home>/workspaces/<id>/work):
//     `agents` is NOT re-admitted — during a workspace turn an agent's own home
//     is as unreachable as anyone else's, matching today's behaviour exactly.
//
// An empty workDir re-admits nothing, which is the safe direction: it yields
// the full set.
func DeniedPathsFor(home, workDir string) []string {
	if home == "" {
		return nil
	}
	clean := filepath.Clean(home)
	out := make([]string, 0, len(SecretEntriesRelative)+4)
	for _, name := range SecretEntriesAlways {
		out = append(out, filepath.Join(clean, name))
	}
	for _, name := range SecretEntriesPerTurn {
		root := filepath.Join(clean, name)
		if workDir != "" && isProperDescendant(filepath.Clean(workDir), root) {
			continue // own tree — re-admitted
		}
		out = append(out, root)
	}
	return append(out, secretBackupPaths(clean)...)
}

// isProperDescendant reports whether child is strictly inside parent. Equality
// is NOT a descendant: a work dir sitting exactly ON a carve-out root does not
// earn the exception, or pointing a turn at `agents/` itself would re-admit
// every agent's home at once.
func isProperDescendant(child, parent string) bool {
	if child == parent {
		return false
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

// IsSecretName reports whether a $OMNIPUS_HOME-relative entry name is a secret,
// by exact match or backup prefix. Used by the Linux sibling-granting walk,
// which decides per directory entry whether to grant it.
func IsSecretName(name string) bool {
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
