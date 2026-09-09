// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import "github.com/elicify-ai/omnipus/pkg/fspolicy"

// SecretEntriesRelative and the helpers below ALIAS the single definition in
// pkg/fspolicy. ADR-063 D3 / spec FR-3.1.
//
// # Why the definition is over there and not here
//
// It is data about the $OMNIPUS_HOME layout, so the leaf package is where it
// belongs on merit — but it is also the only place it CAN live. pkg/tools
// imports pkg/sandbox, and ADR-046's P3 wires pkg/sandbox -> pkg/fspolicy, so a
// definition here that fspolicy had to import would be an import cycle. The
// first draft of the ADR-063 spec specified exactly that and would not have
// compiled.
//
// The set is the UNION of what the two layers used to protect separately. Each
// half covered something the other missed:
//
//	master.key, credentials.json  both        root of trust
//	config.json, cli.token        kernel only  sandbox self-disable; live token
//	entities                      both         per-agent tool policy
//	agents, workspaces            app only     cross-agent isolation
//	config.json.bak-* etc.        kernel only  a copy of a secret is a secret
//	auth.json, backups            NEITHER      plaintext OAuth tokens; a tarball
//	                                           of the entire vault
//
// These aliases exist so no call site in this package churns. There is still
// exactly one list.
var SecretEntriesRelative = fspolicy.SecretEntriesRelative

// SecretFilesRelative is the historical name for the same set, retained from
// v0.2 #155 item 8 (pentest items C1/C2) so older references keep compiling.
var SecretFilesRelative = fspolicy.SecretEntriesRelative

// SecretEntriesAlwaysRelative is the context-free half — the entries excluded
// with no work dir to compare against. This is what a BOOT-time carve-out must
// iterate; using the combined list there strips `agents/` and `workspaces/`
// wholesale and makes every agent's own working directory unreachable.
//
// # It is BOTH context-free lists, and the omission was a real bug
//
// This aliased fspolicy.SecretEntriesAlways alone until ADR-072 D10.3.
// SecretEntriesAlwaysPathOnly (`skills`) had been added as a THIRD list
// (ADR-072 D10.1) and nothing updated this alias, so DefaultChildPolicy —
// this variable's only consumer — granted a spawned child full RWX on
// $OMNIPUS_HOME/skills while every other context-free secret was carved out.
// Its own test asserted that grant as CORRECT, which is how it stayed
// invisible.
//
// Not exploitable when it was found (DefaultChildPolicy has no production
// caller yet; see its own "production wiring is NOT yet active" note), which
// is precisely why it is fixed now rather than left to be discovered by the
// v0.3 change that wires it up.
//
// The distinction fspolicy draws between its two context-free lists is about
// which CONSUMERS read them — pkg/tools/shell.go's literal-text guard and the
// app-layer carve-out list read only SecretEntriesAlways — and this is
// neither of those: it is a KERNEL carve-out, where both lists apply
// identically. Same reasoning as fspolicy.SecretPathsAlways, which unions the
// two for the same reason.
//
// A fresh slice, not an append onto fspolicy's own backing array: appending
// to a package-level slice from another package can write into that slice's
// spare capacity and silently mutate it for every other reader.
var SecretEntriesAlwaysRelative = func() []string {
	out := make([]string, 0, len(fspolicy.SecretEntriesAlways)+len(fspolicy.SecretEntriesAlwaysPathOnly))
	out = append(out, fspolicy.SecretEntriesAlways...)
	return append(out, fspolicy.SecretEntriesAlwaysPathOnly...)
}()

// SecretPaths returns the context-free part of the set — the entries no agent
// may reach in any shape.
//
// It deliberately maps to fspolicy.SecretPathsAlways, NOT fspolicy.SecretPaths.
// The kernel policy is computed at BOOT, where there is no work dir, and the
// full set includes `agents/` and `workspaces/` whose exclusion is only
// meaningful relative to one. Denying them here would lock every agent out of
// its own working directory. Per-turn denial of those roots is
// fspolicy.DeniedPathsFor's job, applied where a work dir is known.
func SecretPaths(homePath string) []string { return fspolicy.SecretPathsAlways(homePath) }

// DeniedPathsFor is the per-turn form: the context-free entries plus the
// coarse roots minus the caller's own tree. See fspolicy.DeniedPathsFor.
func DeniedPathsFor(home, workDir string) []string {
	return fspolicy.DeniedPathsFor(home, workDir)
}

// SecretBackupPathPrefixes is the path-PREFIX form of the secret set: every
// path starting with one of these is a backup copy of a secret. See
// fspolicy.SecretBackupPathPrefixes for why a prefix and not a path list.
func SecretBackupPathPrefixes(home string) []string {
	return fspolicy.SecretBackupPathPrefixes(home)
}
