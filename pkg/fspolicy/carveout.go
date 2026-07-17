package fspolicy

import (
	"path/filepath"
	"strings"
)

// buildCarveOuts returns the fixed, $OMNIPUS_HOME-anchored carve-out roots
// that are always denied regardless of scope (FR-017): the master key file,
// the encrypted credential store, and the whole agents/ and workspaces/
// subtrees. These are whole-subtree roots — never enumerated per agent id or
// workspace id.
//
// omnipusHome MUST already be an absolute, realpath'd location; the caller,
// EffectiveFSPolicy, resolves it before calling this.
func buildCarveOuts(omnipusHome string) []string {
	return []string{
		filepath.Join(omnipusHome, "master.key"),
		filepath.Join(omnipusHome, "credentials.json"),
		filepath.Join(omnipusHome, "agents"),
		filepath.Join(omnipusHome, "workspaces"),
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
// Prefix checks use a trailing-separator guard (matching isCrossAgentPath's
// existing precedent) so e.g. "/a/bc" is never mistaken for a descendant of
// "/a/b".
func IsCarveOut(resolvedAbsPath string, policy FSPolicy) bool {
	cleanPath := filepath.Clean(resolvedAbsPath)

	underCarveOut := false
	for _, root := range policy.CarveOuts {
		if isWithinOrEqual(cleanPath, filepath.Clean(root)) {
			underCarveOut = true
			break
		}
	}
	if !underCarveOut {
		return false
	}

	if policy.WorkDir != "" && isWithinOrEqual(cleanPath, filepath.Clean(policy.WorkDir)) {
		return false
	}

	return true
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
