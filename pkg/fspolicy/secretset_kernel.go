// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"fmt"
	"os"
	"path/filepath"
)

// KernelDeniedPathsFor is DeniedPathsFor rendered at the granularity a KERNEL
// policy needs: an explicit path list, precise enough to reproduce what
// IsCarveOut answers per path. ADR-061 FR-3.3 ("carried across EXACTLY").
//
// # Why DeniedPathsFor is not enough on its own
//
// The two layers ask the same question in different shapes. The app layer is
// handed one resolved path at a time and can afford a rule ("is this inside my
// own tree?"). The kernel is handed a policy BEFORE any path exists and needs a
// finite list. DeniedPathsFor bridges that with a whole-root decision: a
// per-turn root containing the work dir is re-admitted ENTIRELY.
//
// For a workspace-rooted turn that is exact — `agents` is denied wholesale and
// nothing is lost. For an AGENT-HOME-rooted turn it is not, and the gap is the
// whole cross-agent boundary:
//
//	work dir            $OMNIPUS_HOME/agents/self
//	IsCarveOut(agents/victim/SOUL.md)   -> true   (denied: not inside my tree)
//	DeniedPathsFor(...)                 -> `agents` re-admitted, so NOT denied
//
// So the kernel granted every other agent's home while the app layer denied it.
// That was not theoretical — it was executed against real children:
// `cat $OMNIPUS_HOME/agents/victim/SOUL.md` and `echo PWNED > …` both succeeded
// from a `bash` call while read_file on the same path was refused.
//
// # What this adds
//
// For each per-turn root R that DeniedPathsFor re-admits, it walks the chain
// from R down to the work dir and denies every entry that is NOT on that chain.
// Given R = <home>/workspaces and workDir = <home>/workspaces/w1/work:
//
//	<home>/workspaces/w2          denied (another workspace)
//	<home>/workspaces/w1/mounts.json  denied (own workspace's RECORD — the app
//	                              layer denies this too: the own-tree exception
//	                              is anchored on the work dir, not the workspace)
//	<home>/workspaces/w1/work     allowed (the work dir itself)
//
// Both backends can honour the result: macOS renders each entry as an explicit
// deny, Linux never grants them while granting their siblings
// (ExpandRulesExcluding). Same list, two mechanisms — the asymmetry
// SandboxPolicy.DeniedPaths already documents.
//
// # Freshness
//
// The listing reflects disk at call time, so an agent created AFTER this call
// is not in the list. That is bounded by the fact that the policy is derived
// per spawn: the next child sees the new directory. It is not bounded for a
// child that is already running, which is inherent — a kernel policy is fixed
// at exec.
//
// # Failure
//
// Returns an error when a directory on the chain cannot be listed. Callers must
// treat that as fail-closed: without the listing there is no way to distinguish
// the caller's own tree from everyone else's, and the only safe answer is to
// deny the root wholesale (which locks the agent out of its own home — loud,
// and the correct direction) or to refuse the spawn. Never fall back to
// DeniedPathsFor's re-admission, which is the wider answer.
func KernelDeniedPathsFor(home, workDir string) ([]string, error) {
	if home == "" {
		return nil, nil
	}
	denied := DeniedPathsFor(home, workDir)
	if workDir == "" {
		// Nothing was re-admitted, so there is nothing to refine.
		return denied, nil
	}

	cleanHome := filepath.Clean(home)
	cleanWork := filepath.Clean(workDir)

	seen := make(map[string]struct{}, len(denied))
	for _, p := range denied {
		seen[p] = struct{}{}
	}

	for _, name := range SecretEntriesPerTurn {
		root := filepath.Join(cleanHome, name)
		if !isProperDescendant(cleanWork, root) {
			// Not re-admitted — DeniedPathsFor already denied the whole root.
			continue
		}
		siblings, err := siblingsOutsideOwnBranch(root, cleanWork)
		if err != nil {
			return nil, fmt.Errorf(
				"fspolicy: cannot enumerate %q to separate the caller's own tree from the others: %w", root, err)
		}
		for _, p := range siblings {
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			denied = append(denied, p)
		}
	}
	return denied, nil
}

// KernelDeniedNodesFor returns the DIRECTORY NODES a turn must not operate on,
// even though their children are reachable: every directory on the chain from a
// re-admitted per-turn root down to (but excluding) the work dir.
//
// For work dir <home>/agents/self that is [<home>/agents]; for
// <home>/workspaces/w1/work it is [<home>/workspaces, <home>/workspaces/w1].
//
// # Why a node is not the same as a subtree
//
// KernelDeniedPathsFor denies SUBTREES, and it cannot deny these: denying the
// <home>/agents subtree would deny the agent its own home, which is the whole
// reason the root is re-admitted. But the node itself still has to be off
// limits, and the app layer already treats it that way — IsCarveOut(<home>/agents)
// is true for an agent-home-rooted turn, because the own-tree exception requires
// the path to be inside the work dir and the parent directory is not.
//
// Three operations act on the node rather than on its children, and the first
// is a complete bypass of everything above:
//
//	mv  <home>/agents <home>/agents-old   relocates every agent's home to a
//	                                      path that no deny covers, after which
//	                                      the child reads all of them
//	mkdir <home>/agents/fake              fabricates an agent home
//	ls  <home>/agents                     enumerates every agent by name
//
// The rename was executed against a real child and SUCCEEDED before this
// existed. It is the same shape as the "a read-only deny is defeated by
// rename(2)" note in the Seatbelt renderer, one level up the tree.
//
// # Platform consumption
//
// macOS renders these as LITERAL denies — measured to block ls/mkdir/rename on
// the node while leaving children fully readable and writable, which is exactly
// the shape required. Linux needs nothing: ExpandRulesExcluding enumerates a
// directory that contains a denied descendant and grants its children
// individually, never the directory itself, so the node is already ungranted
// there. Same outcome, two mechanisms — the asymmetry SandboxPolicy.DeniedPaths
// already documents for the subtree list.
//
// Pure: unlike KernelDeniedPathsFor this needs no listing, because the chain is
// derivable from the work-dir path alone.
func KernelDeniedNodesFor(home, workDir string) []string {
	if home == "" || workDir == "" {
		return nil
	}
	cleanHome := filepath.Clean(home)
	cleanWork := filepath.Clean(workDir)

	var out []string
	for _, name := range SecretEntriesPerTurn {
		root := filepath.Join(cleanHome, name)
		if !isProperDescendant(cleanWork, root) {
			// Not re-admitted: the whole subtree is denied already, and a node
			// deny on top would be redundant.
			continue
		}
		rel, err := filepath.Rel(root, cleanWork)
		if err != nil {
			continue
		}
		current := root
		out = append(out, current)
		components := splitPathComponents(rel)
		// The final component IS the work dir, which must stay operable.
		for i := 0; i < len(components)-1; i++ {
			current = filepath.Join(current, components[i])
			out = append(out, current)
		}
	}
	return out
}

// siblingsOutsideOwnBranch returns every entry under root that is NOT on the
// path from root down to workDir.
//
// It walks one directory level at a time rather than recursing: once an entry is
// off the chain, denying that entry covers its whole subtree, so there is
// nothing below it worth visiting. The walk therefore costs one ReadDir per
// component of the work dir's path below root — two, for both shapes the
// product actually produces.
//
// workDir MUST be a proper descendant of root; the caller checks that.
func siblingsOutsideOwnBranch(root, workDir string) ([]string, error) {
	rel, err := filepath.Rel(root, workDir)
	if err != nil {
		return nil, fmt.Errorf("relate %q to %q: %w", workDir, root, err)
	}

	var out []string
	current := root
	for _, onChain := range splitPathComponents(rel) {
		entries, readErr := os.ReadDir(current)
		if readErr != nil {
			// A chain component that does not exist yet (a workspace whose
			// work/ dir is created lazily) is not an error: there are no
			// siblings to deny inside a directory that is not there.
			if os.IsNotExist(readErr) {
				return out, nil
			}
			return nil, fmt.Errorf("list %q: %w", current, readErr)
		}
		for _, e := range entries {
			if e.Name() == onChain {
				continue
			}
			out = append(out, filepath.Join(current, e.Name()))
		}
		current = filepath.Join(current, onChain)
	}
	return out, nil
}

// splitPathComponents splits a cleaned relative path into its components.
// Returns nil for "." (no components).
func splitPathComponents(rel string) []string {
	rel = filepath.Clean(rel)
	if rel == "." || rel == string(filepath.Separator) {
		return nil
	}
	var out []string
	for rel != "" && rel != "." && rel != string(filepath.Separator) {
		dir, base := filepath.Split(rel)
		if base != "" {
			out = append([]string{base}, out...)
		}
		rel = filepath.Clean(dir)
		if dir == "" {
			break
		}
	}
	return out
}
