// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KernelDeniedPathsFor is DeniedPathsFor rendered at the granularity a KERNEL
// policy needs: an explicit path list, precise enough to reproduce what
// IsCarveOut answers per path. ADR-063 FR-3.3 ("carried across EXACTLY").
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
		// isProperDescendant answers by filesystem IDENTITY; the chain walk
		// below is LEXICAL. Re-anchor the root into the spelling that is a
		// lexical ancestor of the work dir before walking. See chainRootFor.
		chainRoot, err := chainRootFor(root, cleanWork)
		if err != nil {
			return nil, fmt.Errorf(
				"fspolicy: cannot anchor %q against work dir %q: %w", root, cleanWork, err)
		}
		siblings, err := siblingsOutsideOwnBranch(chainRoot, cleanWork)
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

// KernelDeniedNodesFor returns the DIRECTORY NODES a turn must not WRITE to,
// even though their children stay fully readable and writable: every directory
// on the chain from a re-admitted per-turn root down to (but excluding) the
// work dir.
//
// For work dir <home>/agents/self that is [<home>/agents]; for
// <home>/workspaces/w1/work it is [<home>/workspaces, <home>/workspaces/w1].
//
// # Why a node is not the same as a subtree
//
// KernelDeniedPathsFor denies SUBTREES, and it cannot deny these: denying the
// <home>/agents subtree would deny the agent its own home, which is the whole
// reason the root is re-admitted. But the node itself still has to be off
// limits for WRITES, and the app layer already treats it that way —
// IsCarveOut(<home>/agents) is true for an agent-home-rooted turn, because the
// own-tree exception requires the path to be inside the work dir and the parent
// directory is not.
//
// The operation that matters is rename(2) ON THE NODE, and it is a complete
// bypass of every deny above it:
//
//	mv <home>/agents <home>/agents-old
//
// relocates every agent's home to a path no deny covers, after which the child
// reads all of them. Measured against a real child under /usr/bin/sandbox-exec,
// through the production profile shape (RWX on $OMNIPUS_HOME plus per-sibling
// denies), BEFORE this list was consumed by anything:
//
//	cat  <home>/agents/victim/SOUL.md                 -> Operation not permitted
//	mv   <home>/agents <home>/agents-old              -> SUCCEEDED
//	cat  <home>/agents-old/victim/SOUL.md             -> VICTIM-SOUL-SECRET
//
// It is the same shape as the "a read-only deny is defeated by rename(2)" note
// in the Seatbelt renderer, one level up the tree.
//
// # Platform consumption
//
// macOS renders each node as (deny file-write* (literal ...)) — a LITERAL, not
// a subpath, so it covers the directory entry itself and nothing beneath it.
// Measured on Darwin 25.5.0 with the same real children:
//
//	mv    <home>/agents <home>/agents-old   -> denied  (this is the fix)
//	cat   <home>/agents/self/OWN.md         -> allowed (own tree, unaffected)
//	echo >> <home>/agents/self/OWN.md       -> allowed (own tree, unaffected)
//	touch <home>/agents/self/new            -> allowed (own tree, unaffected)
//
// Linux needs nothing: ExpandRulesExcluding enumerates a directory that
// contains a denied descendant and grants its children individually, never the
// directory itself, so the node is already ungranted there — and Landlock's
// REFER right is required to rename ACROSS directories, which the policy never
// grants. Same outcome, two mechanisms — the asymmetry SandboxPolicy.DeniedPaths
// already documents for the subtree list.
//
// # What this does NOT do — measured, not assumed
//
// A literal write-deny on the node leaves two operations open, and both were
// re-measured on Darwin 25.5.0 rather than inferred:
//
//	ls    <home>/agents        -> ALLOWED. Deliberate. Seatbelt checks a read
//	                             against the directory the child is listing, so
//	                             denying it needs (deny file-read* (literal ...))
//	                             — and a child must be able to traverse this
//	                             node to reach its own work dir underneath it.
//	                             The residual is a list of agent/workspace NAMES,
//	                             already documented as the one accepted
//	                             divergence in
//	                             TestKernelDeniedPaths_MatchIsCarveOutPathForPath.
//	mkdir <home>/agents/fake   -> ALLOWED. Seatbelt checks a create against the
//	                             CHILD path, which is not the literal. Closing
//	                             it needs a subtree deny with a require-not
//	                             exemption for the caller's own branch, which is
//	                             a different rendering shape than this node list.
//	                             Linux does NOT have this residual: MAKE_DIR on
//	                             <home>/agents is never granted there.
//
// An earlier version of this comment claimed the literal deny was "measured to
// block ls/mkdir/rename". Only rename is true. The claim was written before the
// list was consumed by anything, so nothing contradicted it.
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
		// Same identity-vs-lexical hazard as KernelDeniedPathsFor: re-anchor
		// before computing a relative chain. This function has no error
		// return, so an unanchorable root degrades to denying writes on the
		// root NODE alone — narrower than the full chain, and never the
		// upward walk that a `..`-bearing relative path would produce.
		chainRoot, anchorErr := chainRootFor(root, cleanWork)
		if anchorErr != nil {
			out = append(out, root)
			continue
		}
		components, relErr := descendingComponents(chainRoot, cleanWork)
		if relErr != nil {
			out = append(out, chainRoot)
			continue
		}
		current := chainRoot
		out = append(out, current)
		// The final component IS the work dir, which must stay operable.
		for i := 0; i < len(components)-1; i++ {
			current = filepath.Join(current, components[i])
			out = append(out, current)
		}
	}
	return out
}

// chainRootFor returns the spelling of root that is a LEXICAL ancestor of
// workDir, so a caller can safely walk down from it with filepath.Rel.
//
// # The bug this exists to make impossible
//
// The callers decide "is workDir inside root?" with isProperDescendant, which
// answers by FILESYSTEM IDENTITY (os.SameFile) and therefore says yes even when
// the two paths are spelled differently — which they routinely are. WorkDir
// arrives symlink-resolved (fspolicy.EffectiveFSPolicy calls realpath on it),
// while root is built from $OMNIPUS_HOME exactly as the operator wrote it. On
// macOS /tmp is a firmlink to /private/tmp, so an $OMNIPUS_HOME of
// /tmp/omnipus-home yields root=/tmp/omnipus-home/agents and
// workDir=/private/tmp/omnipus-home/agents/self.
//
// filepath.Rel of those two is "../../../private/tmp/omnipus-home/agents/self".
// Walking that chain does not descend — it ASCENDS, one ReadDir per "..", and
// the callers deny every entry they meet on the way. MEASURED on Darwin 25.5.0
// against the real product path, with $OMNIPUS_HOME=/tmp/omnipus-uat-home: the
// rendered Seatbelt profile grew from ~50 KB to 663 KB / 12,184 deny rules and
// contained
//
//	(deny file-read* (subpath "/bin"))     (deny file-read* (subpath "/usr"))
//	(deny file-read* (subpath "/System"))  (deny file-read* (subpath "/private/var"))
//	(deny file-write* (literal "/"))       (deny file-write* (literal "/private"))
//
// i.e. the secret set had swallowed the entire filesystem. Every child then
// failed with EPERM on paths the same profile plainly allowed a few lines
// earlier — "sh: ls: command not found", "Error opening /private/var/select/sh",
// and "getcwd: cannot access parent directories" on every single invocation.
// The blanket (allow file-read*) of the ADR-062 open model does not save it:
// per the measured precedence table in seatbelt_profile.go, an UNFILTERED
// blanket allow never overrides a filtered deny, in either order.
//
// Nothing reported any of this. The profile rendered, sandbox-exec accepted it,
// and the boot log said the sandbox was active.
//
// # The rule
//
// Prefer the declared spelling; fall back to the symlink-resolved one; refuse
// if neither is a lexical ancestor. Refusing is the fail-closed direction: the
// callers turn it into "deny this root wholesale" (KernelDeniedPathsFor's error
// contract) or "deny the root node only" (KernelDeniedNodesFor), never into an
// upward walk.
func chainRootFor(root, workDir string) (string, error) {
	if isLexicalAncestor(root, workDir) {
		return root, nil
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err == nil && isLexicalAncestor(resolved, workDir) {
		return resolved, nil
	}
	return "", fmt.Errorf(
		"work dir is inside the root by filesystem identity but neither the declared spelling %q "+
			"nor its resolved form is a lexical ancestor of %q (a relative walk would ascend out of the root)",
		root, workDir)
}

// isLexicalAncestor reports whether dir is a strict prefix DIRECTORY of p,
// comparing path text only. Both arguments must already be cleaned.
func isLexicalAncestor(dir, p string) bool {
	if dir == "" || p == "" || dir == p {
		return false
	}
	sep := string(filepath.Separator)
	if dir == sep {
		return strings.HasPrefix(p, sep) && len(p) > 1
	}
	return strings.HasPrefix(p, dir+sep)
}

// descendingComponents returns the path components leading from root DOWN to
// workDir, refusing any chain that would step outside root.
//
// The ".." check is the backstop for chainRootFor: even if a future caller
// forgets to anchor, or a path form nobody anticipated slips through, this
// function fails rather than handing back a chain that walks up to "/". A deny
// list is a security control, and one that silently grows to cover the whole
// filesystem is not a stricter control — it is a broken one.
func descendingComponents(root, workDir string) ([]string, error) {
	rel, err := filepath.Rel(root, workDir)
	if err != nil {
		return nil, fmt.Errorf("relate %q to %q: %w", workDir, root, err)
	}
	components := splitPathComponents(rel)
	for _, c := range components {
		if c == ".." {
			return nil, fmt.Errorf(
				"relative path %q from %q to %q escapes the root; refusing to walk upward", rel, root, workDir)
		}
	}
	return components, nil
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
	// descendingComponents, not a bare filepath.Rel: a chain containing ".."
	// makes this loop ReadDir its way UP to "/" and deny every entry of every
	// directory on the way. See chainRootFor for the measured consequences.
	components, err := descendingComponents(root, workDir)
	if err != nil {
		return nil, err
	}

	var out []string
	current := root
	for _, onChain := range components {
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
