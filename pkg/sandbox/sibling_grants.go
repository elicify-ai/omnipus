// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ExpandRulesExcluding rewrites a rule list so that none of deniedPaths falls
// inside any granted tree, WITHOUT using a deny primitive. ADR-060 §4.2, spec
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
// enumeration defect ADR-060 exists to remove: coverage would then depend on
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
func ExpandRulesExcluding(rules []PathRule, deniedPaths []string) ([]PathRule, error) {
	if len(deniedPaths) == 0 {
		return rules, nil
	}
	denied := make([]string, 0, len(deniedPaths))
	for _, d := range deniedPaths {
		if d == "" {
			continue
		}
		denied = append(denied, filepath.Clean(d))
	}
	if len(denied) == 0 {
		return rules, nil
	}

	out := make([]PathRule, 0, len(rules)+len(denied)*4)
	for _, rule := range rules {
		path := filepath.Clean(rule.Path)

		// The rule itself is (or is inside) something that must be unreachable.
		if isAtOrUnderAny(path, denied) {
			continue
		}

		inside := deniedStrictlyUnder(path, denied)
		if len(inside) == 0 {
			out = append(out, rule)
			continue
		}

		expanded, err := siblingGrants(PathRule{Path: path, Access: rule.Access}, inside)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// siblingGrants replaces one grant with grants on everything it contains except
// the paths leading to blocked. blocked must be strictly under rule.Path.
func siblingGrants(rule PathRule, blocked []string) ([]PathRule, error) {
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
