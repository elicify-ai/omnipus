package fspolicy

import (
	"path/filepath"
	"strings"
)

// buildCarveOuts returns the fixed, $OMNIPUS_HOME-anchored carve-out roots
// that are always denied regardless of scope (FR-017): the master key file,
// the encrypted credential store, and the whole agents/, workspaces/, and
// entities/ subtrees. These are whole-subtree roots — never enumerated per
// agent id or workspace id.
//
// entities/ (ADR-054 D2/D4, added 2026-07-25) holds the per-entity JSON
// records pkg/entity persists (starting with entities/agents/<id>.json — the
// AgentConfig record split out of config.json). This carve-out is CRITICAL
// and non-negotiable: an AgentConfig record carries the Constraint #6 tool-
// policy map, Locked, and Default. Without entities/ on this list, an agent
// running under FSScopeUnrestricted could rewrite EVERY agent's security
// record directly — granting itself every tool, clearing Locked, or setting
// Default:true to hijack routing — which is strictly worse than the
// agents/-only carve-out this replaces (that at least shielded other
// agents' directories). See ADR-054 §4 ("MANDATORY — entities must be added
// to the carve-out list").
//
// omnipusHome MUST already be an absolute, realpath'd location; the caller,
// EffectiveFSPolicy, resolves it before calling this.
func buildCarveOuts(omnipusHome string) []string {
	return []string{
		filepath.Join(omnipusHome, "master.key"),
		filepath.Join(omnipusHome, "credentials.json"),
		filepath.Join(omnipusHome, "agents"),
		filepath.Join(omnipusHome, "workspaces"),
		filepath.Join(omnipusHome, "entities"),
	}
}

// IsCarveOut reports whether resolvedAbsPath falls on or under any of
// policy.CarveOuts.
//
// This replaces the broken isCrossAgentPath (pkg/tools/filesystem.go:98-115),
// whose bug was deriving agentsRoot = filepath.Dir(absWorkspace) — correct
// only when the working dir happened to be exactly agents/<id>/, and
// silently permissive under a re-rooted workspace turn (where the working
// dir is workspaces/<id>/work/ instead). IsCarveOut instead anchors
// exclusively on policy.CarveOuts, which EffectiveFSPolicy always builds
// from the boot-known $OMNIPUS_HOME — never derived from the (re-rootable)
// working directory.
//
// resolvedAbsPath MUST already be realpath'd and filepath.Clean'd by the
// caller (typically the ResolvePath resolver) — this function performs no
// I/O and no further symlink resolution of its own.
//
// Own-tree exception: a path that is also within policy.WorkDir is never
// treated as a carve-out, even when it falls under a coarse carve-out root
// (agents/ or workspaces/). This matters for the agent-home-rooted WorkDir
// shape (WorkDir == <home>/agents/<self>) — the agent's own home must not be
// a carve-out of itself. It does NOT change anything for the re-rooted
// workspace-turn shape (WorkDir == <home>/workspaces/<id>/work) — there,
// agents/<self>/ falls outside WorkDir just like any other agent's home, so
// the agent's own home stays exactly as unreachable as anyone else's during
// a workspace turn, matching today's re-root behavior.
//
// BLOCK #2 (ADR-046 P1 review): the own-tree exception is scoped PER-ROOT —
// it only exempts a path matched against carve-out root R when WorkDir is a
// PROPER DESCENDANT of that SAME root R (WorkDir != R, WorkDir strictly
// under R). A naive "cleanPath is within-or-equal WorkDir" check (with no
// relationship required between WorkDir and R) is defeatable by a
// misconfigured WorkDir: if WorkDir == $OMNIPUS_HOME itself, every one of
// policy.CarveOuts (all direct children of $OMNIPUS_HOME) would sit "within
// WorkDir" simultaneously, exempting master.key/credentials.json/agents/
// workspaces all at once — the exact bypass this fix closes.
// FSPolicy.Validate (policy.go) independently refuses to construct a policy
// shaped that way, but IsCarveOut stays correct here too, in depth, since
// callers besides ResolvePath may invoke it directly.
//
// Prefix checks use a trailing-separator guard (matching isCrossAgentPath's
// existing precedent) so e.g. "/a/bc" is never mistaken for a descendant of
// "/a/b".
func IsCarveOut(resolvedAbsPath string, policy FSPolicy) bool {
	cleanPath := filepath.Clean(resolvedAbsPath)
	cleanWorkDir := filepath.Clean(policy.WorkDir)

	for _, root := range policy.CarveOuts {
		cleanRoot := filepath.Clean(root)
		if !isWithinOrEqual(cleanPath, cleanRoot) {
			continue
		}

		// cleanPath falls on or under this carve-out root. The own-tree
		// exception applies only when WorkDir is a proper descendant of
		// THIS SAME root (never when WorkDir is at or above it) AND
		// cleanPath itself falls within that WorkDir.
		if policy.WorkDir != "" &&
			cleanWorkDir != cleanRoot &&
			isWithinOrEqual(cleanWorkDir, cleanRoot) &&
			isWithinOrEqual(cleanPath, cleanWorkDir) {
			return false
		}

		return true
	}

	return false
}

// isWithinOrEqual reports whether candidate is root itself or lives strictly
// under it, guarded by a trailing separator so "/a/bc" does not match root
// "/a/b".
func isWithinOrEqual(candidate, root string) bool {
	if candidate == root {
		return true
	}
	rootWithSep := root
	if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
		rootWithSep += string(filepath.Separator)
	}
	return strings.HasPrefix(candidate, rootWithSep)
}
