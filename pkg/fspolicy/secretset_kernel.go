// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"errors"
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
func KernelDeniedPathsFor(home, workDir string, extraAllowed ...string) ([]string, error) {
	if home == "" {
		return nil, nil
	}
	branches := make([]KernelBranch, 0, 1+len(extraAllowed))
	if workDir != "" {
		branches = append(branches, KernelBranch{Path: workDir, Writable: true})
	}
	for _, extra := range extraAllowed {
		if extra == "" {
			continue
		}
		branches = append(branches, KernelBranch{Path: extra, Writable: true})
	}
	denied, _, err := KernelDenyEnumeration(home, workDir, branches)
	return denied, err
}

// KernelBranch is one allowed branch of the kernel enumeration: a path under
// $OMNIPUS_HOME whose subtree stays reachable for the child, and the access
// class it keeps. Writable=false marks a read/execute-only branch — the
// enumeration reports it in the write-denied list so the kernel layer can
// withhold write (Landlock: the branch rule carries no write right; Seatbelt:
// an explicit write deny).
type KernelBranch struct {
	Path     string
	Writable bool
}

// KernelDenyEnumeration is the generalised per-spawn deny set (amendment §2
// D5): children of $OMNIPUS_HOME outside every allowed branch are denied,
// recursively down to the branch roots, with the named protected inventory
// (ProtectedInventoryNames) always denied on top. It replaces the old
// per-turn-root refinement for every per-turn caller.
//
// The workDir and every extra branch keep their subtrees reachable: the walk
// descends only along chains to branch roots, denies every entry it meets
// that is not on such a chain and not itself a branch root, and stops at
// branch roots (their own grant rules govern their interiors; deeper named
// denials, e.g. agents/<id>/.omnipus inside an agent-home-rooted turn, come
// from the named inventory, which is evaluated independently of the walk).
//
// Named inventory entries are denied whether or not they exist on disk, so a
// lazily-created home entry (logs/, backups/) is covered before its first
// write. Entries the gateway creates AFTER this enumeration are covered on
// Linux by the grant shape (the home itself is never granted, so a new entry
// carries no grant) and remain the documented macOS post-spawn residual.
//
// writeDenied carries the read-only branch roots (media, the session-scoped
// upload dirs, read-only runtime/document allowances): subtrees that stay
// readable but must never gain a write right. macOS renders them as explicit
// write denies; on Linux the branch's own rule simply carries no write bit,
// and the enumeration never grants them wholesale from an ancestor rule.
//
// Enumeration failure (a chain directory that cannot be listed) returns an
// error and the caller must fail closed — the same contract
// KernelDeniedPathsFor has always had (FR-4.5c).
func KernelDenyEnumeration(home, workDir string, branches []KernelBranch) (denied, writeDenied []string, err error) {
	if home == "" {
		return nil, nil, nil
	}
	cleanHome := filepath.Clean(home)

	// Normalise the branch set: absolute, cleaned, and anchored under the
	// home (branches outside the home are none of this enumeration's
	// business — /tmp, workspace mounts elsewhere and operator paths are
	// governed by their own grant rules).
	branchRoots := make([]string, 0, len(branches)+1)
	branchWritable := make(map[string]bool, len(branches)+1)
	addBranch := func(path string, writable bool) {
		clean := filepath.Clean(path)
		if clean == "" || clean == "." || !filepath.IsAbs(clean) {
			return
		}
		if !CoversForDeny(cleanHome, clean) {
			return
		}
		if _, dup := branchWritable[clean]; dup {
			// Writable wins: the widest explicit grant for one root governs.
			branchWritable[clean] = branchWritable[clean] || writable
			return
		}
		branchRoots = append(branchRoots, clean)
		branchWritable[clean] = writable
	}
	if workDir != "" {
		addBranch(workDir, true)
	}
	for _, b := range branches {
		addBranch(b.Path, b.Writable)
	}

	isBranchRoot := func(p string) bool {
		_, ok := branchWritable[p]
		return ok
	}
	underBranchRoot := func(p string) bool {
		for _, root := range branchRoots {
			if CoversForDeny(root, p) {
				return true
			}
		}
		return false
	}
	onChainToBranch := func(p string) bool {
		for _, root := range branchRoots {
			if !SameLocationForDeny(p, root) && CoversForDeny(p, root) {
				return true
			}
		}
		return false
	}

	seen := make(map[string]struct{}, 64)
	addDenied := func(p string) {
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		denied = append(denied, p)
	}

	// 1. The named inventory, always — except an entry that IS a branch root
	// (a branch re-admits its own root; deeper inventory names inside it are
	// handled by their own named entries).
	for _, name := range ProtectedInventoryNames {
		p := filepath.Join(cleanHome, name)
		if isBranchRoot(filepath.Clean(p)) {
			continue
		}
		addDenied(filepath.Clean(p))
	}

	// 1b. D4 agent-home-internal entries inside an allowed branch: an
	// agent-home-rooted turn re-admits its own home, but .omnipus/**,
	// memory/, sessions/, skills/ and the definition files stay protected —
	// the same list the app predicate's own-tree narrowing uses, so both
	// layers answer identically.
	for _, root := range branchRoots {
		if !isAgentHomeRoot(cleanHome, root) {
			continue
		}
		for _, name := range AgentHomeInternalDirs {
			addDenied(filepath.Join(root, name))
		}
		for _, name := range AgentHomeInternalFiles {
			addDenied(filepath.Join(root, name))
		}
	}

	// 2. The recursive walk from the home down the chains to the branch
	// roots. The walk is LEXICAL (readdir names), while branch membership is
	// judged by path containment; EffectiveFSPolicy realpaths both the home
	// and the work dir, so the two spellings agree in every production shape.
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				return nil // nothing to enumerate inside an absent directory
			}
			return fmt.Errorf("fspolicy: cannot enumerate %q to build the protected deny set: %w", dir, readErr)
		}
		for _, e := range entries {
			full := filepath.Clean(filepath.Join(dir, e.Name()))
			if isBranchRoot(full) {
				continue // granted by the branch's own rules
			}
			if underBranchRoot(full) {
				// Inside a deeper branch: its grant rules govern; the named
				// inventory above carried the interior denials.
				continue
			}
			if onChainToBranch(full) {
				if err := walk(full); err != nil {
					return err
				}
				continue
			}
			addDenied(full) // default-deny subtree
		}
		return nil
	}
	if err := walk(cleanHome); err != nil {
		return nil, nil, err
	}

	// 3. Read-only branch roots: readable, never writable.
	for _, root := range branchRoots {
		if !branchWritable[root] {
			writeDenied = append(writeDenied, root)
		}
	}
	return denied, writeDenied, nil
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
func KernelDeniedNodesFor(home, workDir string, extraBranchRoots ...string) []string {
	if home == "" {
		return nil
	}
	cleanHome := filepath.Clean(home)

	// The home itself is a denied node (amendment §2 D5): reachable — every
	// branch lives underneath it — but never granted, so its directory entry
	// cannot be renamed and a Linux child is never handed the home wholesale.
	out := []string{cleanHome}

	// Chain nodes for each allowed branch: every directory from the home down
	// to (but excluding) the branch root. For the work-dir branch this is the
	// list this function has always produced; the extra branch roots (runtime
	// and document allowances) add their own chains, so an ancestor such as
	// toolchains/ is enumerated and never granted wholesale — without that, a
	// parent-rule explosion would hand a read-only generation write access.
	roots := make([]string, 0, 1+len(extraBranchRoots))
	if workDir != "" {
		roots = append(roots, filepath.Clean(workDir))
	}
	for _, extra := range extraBranchRoots {
		if extra != "" {
			roots = append(roots, filepath.Clean(extra))
		}
	}

	seen := make(map[string]struct{}, len(out)+len(roots)*4)
	for _, p := range out {
		seen[p] = struct{}{}
	}
	for _, branchRoot := range roots {
		if !CoversForDeny(cleanHome, branchRoot) || SameLocationForDeny(branchRoot, cleanHome) {
			// Not under the home (or the home itself): no chain to record.
			continue
		}
		rel, relErr := filepath.Rel(cleanHome, branchRoot)
		if relErr != nil {
			continue
		}
		// The same identity-vs-lexical hazard chainRootFor documents: a
		// relative chain containing ".." would ASCEND out of the home. Skip
		// the branch rather than emit upward denies — the home node deny
		// above already covers the worst case.
		if strings.HasPrefix(filepath.Clean(rel), "..") {
			continue
		}
		parts := splitPathComponents(rel)
		// parts' last component IS the branch root, which must stay operable.
		current := cleanHome
		for i := 0; i < len(parts)-1; i++ {
			current = filepath.Join(current, parts[i])
			if _, dup := seen[current]; dup {
				continue
			}
			seen[current] = struct{}{}
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
			if errors.Is(readErr, os.ErrNotExist) {
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
