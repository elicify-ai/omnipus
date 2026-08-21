// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ExpandRulesExcluding rewrites a rule list so that none of deniedPaths falls
// inside any granted tree, WITHOUT using a deny primitive. ADR-062 §4.2, spec
// FR-4.5.
//
// # Why this exists
//
// Landlock is grant-only. There is no "deny" — a path is reachable if some
// granted tree contains it, and unreachable otherwise. So where macOS says
// "grant $OMNIPUS_HOME, then deny master.key inside it", Linux has to say
// "grant everything in $OMNIPUS_HOME except master.key", which means naming the
// siblings. Same list, same outcome, different mechanism.
//
// # What it walks, and what it deliberately does not
//
// Only directories ON THE PATH to a denied entry are enumerated. For the real
// secret set that is a single level: five entries directly under $OMNIPUS_HOME.
// Nothing else in the filesystem is listed, so an operator installing a new
// toolchain is still covered automatically by the untouched grants around it.
//
// A full filesystem walk would be both slow and a reintroduction of exactly the
// enumeration defect ADR-062 exists to remove: coverage would then depend on
// having listed every path in advance, which is the thing that cannot be done.
//
// # Failure is not an option here, and that is intentional
//
// A directory listing that fails returns an error, and the caller must abort
// the spawn (FR-4.5c). The tempting fallback — grant the parent and move on —
// would expose the secret while every log line and every test still reads
// green. Refusing to spawn is loud; silently unprotecting a master key is not.
//
// Rules that ARE a denied path, or sit underneath one, are dropped outright.
// Access rights are carried over from the original rule unchanged: this
// function decides reachability, never permission.
//
// # deniedNodes — reachable directories that must never carry a right of their own
//
// deniedNodes (SandboxPolicy.DeniedNodes, from fspolicy.KernelDeniedNodesFor)
// are the directories on the chain down to the work dir: $OMNIPUS_HOME/agents
// for an agent-home-rooted turn, $OMNIPUS_HOME/workspaces and
// $OMNIPUS_HOME/workspaces/<id> for a workspace turn. They must stay REACHABLE
// — the work dir is underneath them — while never being granted THEMSELVES,
// because a write right on a directory is a rename right on it, and renaming
// the node relocates every sibling tree out from under the deny list in one
// syscall.
//
// This was believed to need no code: the walk enumerates any directory that
// contains a denied descendant and grants its children individually, so the
// node is usually skipped as a side effect. "Usually" is the defect. The
// side effect only fires when the node HAS a denied descendant. A workspace
// directory whose mounts.json has not been written yet, or an install with a
// single agent, gives the node no denied descendant at all — and then the walk
// grants the node WHOLESALE with the parent's rights. Measured by
// TestExpandRulesExcluding_NeverGrantsADeniedNode, which failed on the
// workspace shape the first time it was run. Passing them explicitly makes the
// property hold by construction instead of by coincidence of layout.
func ExpandRulesExcluding(rules []PathRule, deniedPaths, deniedNodes, deniedPrefixes []string) ([]PathRule, error) {
	if len(deniedPaths) == 0 && len(deniedNodes) == 0 {
		return rules, nil
	}
	denied := cleanNonEmpty(deniedPaths)
	nodes := cleanNonEmpty(deniedNodes)
	if len(denied) == 0 && len(nodes) == 0 {
		return rules, nil
	}

	prefixes := cleanPrefixes(deniedPrefixes)

	out := make([]PathRule, 0, len(rules)+(len(denied)+len(nodes))*4)
	for _, rule := range rules {
		path := filepath.Clean(rule.Path)

		// The rule itself is (or is inside) something that must be unreachable.
		if isAtOrUnderAny(path, denied) {
			continue
		}

		inside := deniedStrictlyUnder(path, denied)
		nodesInside := deniedStrictlyUnder(path, nodes)

		// A rule whose path IS a node must not be emitted verbatim either: it
		// would hand the node the very right this exists to withhold. Expanding
		// it grants its children instead, which is the same treatment the node
		// gets when it is reached from an ancestor rule.
		ruleIsNode := containsPath(nodes, path)

		if len(inside) == 0 && len(nodesInside) == 0 && !ruleIsNode {
			out = append(out, rule)
			continue
		}

		expanded, err := siblingGrants(PathRule{Path: path, Access: rule.Access}, inside, nodesInside, prefixes)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// cleanNonEmpty Cleans every non-empty entry of paths.
func cleanNonEmpty(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		out = append(out, filepath.Clean(p))
	}
	return out
}

// containsPath reports whether paths contains an entry equal to want.
func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

// siblingGrants replaces one grant with grants on everything it contains except
// the paths leading to blocked. blocked and nodes must be strictly under
// rule.Path.
//
// nodes are directories that must be enumerated (so their children keep their
// grants) but never granted themselves — see ExpandRulesExcluding's contract.
// A traversed directory is skipped when it comes up as a child entry, so
// putting a node in `traverse` is exactly what withholds its rule.
func siblingGrants(rule PathRule, blocked, nodes, prefixes []string) ([]PathRule, error) {
	// traverse: directories to enumerate, i.e. the root plus every ancestor of
	// a blocked path below it. denySet: the blocked paths themselves.
	traverse := map[string]struct{}{rule.Path: {}}
	denySet := make(map[string]struct{}, len(blocked))
	for _, b := range blocked {
		denySet[b] = struct{}{}
		for parent := filepath.Dir(b); len(parent) > len(rule.Path); parent = filepath.Dir(parent) {
			traverse[parent] = struct{}{}
		}
	}
	for _, n := range nodes {
		traverse[n] = struct{}{}
		for parent := filepath.Dir(n); len(parent) > len(rule.Path); parent = filepath.Dir(parent) {
			traverse[parent] = struct{}{}
		}
	}

	dirs := make([]string, 0, len(traverse))
	for d := range traverse {
		dirs = append(dirs, d)
	}
	// Deterministic output. The rule list feeds a kernel ruleset and a test
	// comparison; map iteration order would make both nondeterministic.
	sort.Strings(dirs)

	out := make([]PathRule, 0, len(dirs)*8)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// See the contract above: no fallback. A caller that continued here
			// would grant the parent and expose the secret silently.
			return nil, fmt.Errorf(
				"sandbox: cannot enumerate %q to exclude the secret set; refusing to build a policy "+
					"that would grant it: %w", dir, err)
		}
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			if _, isDenied := denySet[full]; isDenied {
				continue
			}
			if _, isTraversed := traverse[full]; isTraversed {
				// Enumerated in its own right, so granting it wholesale here
				// would re-include the very entry we are excluding.
				continue
			}
			// PREFIX denies, evaluated per entry at ENUMERATION time.
			//
			// denySet holds the exact paths that existed when the policy was
			// BUILT. This walk runs later, at spawn. A backup created in between
			// — `config.json.bak`, which pkg/migrate writes — is by then an
			// existing child, is absent from denySet, and was therefore granted
			// the parent's full access.
			//
			// macOS never had this gap: renderSeatbeltProfile emits
			// DeniedPathPrefixes as anchored regex denies, which match whenever
			// the file appears. Linux enumerates instead, so the same field has
			// to be applied here or it is enforced on one platform only — while
			// two doc comments claimed both were covered.
			if matchesDeniedPrefix(full, prefixes) {
				continue
			}
			out = append(out, PathRule{Path: full, Access: rule.Access})
		}
	}
	return out, nil
}

// deniedStrictlyUnder returns the denied paths that live inside dir.
func deniedStrictlyUnder(dir string, denied []string) []string {
	var out []string
	for _, d := range denied {
		if d != dir && pathIsUnder(d, dir) {
			out = append(out, d)
		}
	}
	return out
}

func isAtOrUnderAny(path string, denied []string) bool {
	for _, d := range denied {
		// pathIsUnder is inclusive of equality, which is what "at or under"
		// wants. It is the package's existing containment helper, reused rather
		// than reimplemented: a second copy of this comparison is exactly the
		// kind of near-duplicate that drifts on the trailing-separator and
		// prefix-segment cases its own tests already cover.
		if pathIsUnder(path, d) {
			return true
		}
	}
	return false
}

// cleanPrefixes normalises the denied-prefix list once, so the per-entry check
// below is a plain comparison rather than repeated allocation inside the walk.
//
// Folded to lower case because this is a DENY-side test: on a case-insensitive
// volume `Config.json.bak` IS the backup, and on a case-sensitive one folding
// merely withholds a grant from a distinctly-named sibling under a directory
// Omnipus owns — the safe direction, with no legitimate claimant.
func cleanPrefixes(prefixes []string) []string {
	if len(prefixes) == 0 {
		return nil
	}
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}

// matchesDeniedPrefix reports whether full begins with any denied prefix.
//
// Prefix rather than exact match by design: the entries these guard are backups
// and rotations whose full names are not knowable in advance
// (`config.json.bak`, `master.key.2026-08-13`, `credentials.json.1`).
func matchesDeniedPrefix(full string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return false
	}
	folded := strings.ToLower(full)
	for _, p := range prefixes {
		if strings.HasPrefix(folded, p) {
			return true
		}
	}
	return false
}
