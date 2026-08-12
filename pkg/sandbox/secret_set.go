// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import "github.com/elicify-ai/omnipus/pkg/fspolicy"

// SecretEntriesRelative and the helpers below ALIAS the single definition in
// pkg/fspolicy. ADR-061 D3 / spec FR-3.1.
//
// # Why the definition is over there and not here
//
// It is data about the $OMNIPUS_HOME layout, so the leaf package is where it
// belongs on merit — but it is also the only place it CAN live. pkg/tools
// imports pkg/sandbox, and ADR-046's P3 wires pkg/sandbox -> pkg/fspolicy, so a
// definition here that fspolicy had to import would be an import cycle. The
// first draft of the ADR-061 spec specified exactly that and would not have
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
var SecretEntriesAlwaysRelative = fspolicy.SecretEntriesAlways

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

// IsSecretEntry reports whether a $OMNIPUS_HOME-relative entry name is in the
// secret set (either half, plus backup prefixes). Used by the Linux
// sibling-granting walk.
func IsSecretEntry(name string) bool { return fspolicy.IsSecretName(name) }
