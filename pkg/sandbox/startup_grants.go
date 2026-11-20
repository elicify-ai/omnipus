// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"path/filepath"
)

// StartupGrantRefusal says why the confined Linux startup rules refuse one
// Office preview startup grant (office-preview spec §3.8 BR-2.3). The zero
// value means the entry is granted.
type StartupGrantRefusal struct {
	// Reason is the operator-facing warning; empty when the entry is granted.
	Reason string
	// WritableFolder is the writable folder the entry lies in or contains;
	// empty for every other refusal.
	WritableFolder string
}

// Refused reports whether the entry gets no rule.
func (r StartupGrantRefusal) Refused() bool { return r.Reason != "" }

// ClassifyStartupGrant judges one startup grant against the folders the
// startup rules make writable, by the refusal rules buildExecPathRules
// applies to sandbox.allowed_exec_paths: an empty or relative path, the
// filesystem root, a restricted system path, and any path that lies inside
// or contains a writable folder (judged on the declared and the
// symlink-resolved path, ignoring letter case — overlapsAny). The gateway
// uses it to tell, after the fact, why a component folder has no rule
// (BR-2.5); buildStartupGrantRules uses it to decide, so the two cannot
// disagree.
func ClassifyStartupGrant(entry string, writableRoots []string) StartupGrantRefusal {
	if entry == "" || !filepath.IsAbs(entry) {
		return StartupGrantRefusal{Reason: "Office preview startup grant is not an absolute path; refused."}
	}
	clean := filepath.Clean(entry)
	if clean == string(filepath.Separator) {
		return StartupGrantRefusal{Reason: "Office preview startup grant is the filesystem root; refused."}
	}
	if isSystemRestricted(clean) {
		return StartupGrantRefusal{Reason: "Office preview startup grant is a restricted system path; refused."}
	}
	if folder, found := firstOverlap(clean, writableRoots); found {
		return StartupGrantRefusal{
			Reason:         "Office preview startup grant lies inside or contains a writable folder (" + folder + "); refused (a writable+executable folder would let an agent run code it just wrote).",
			WritableFolder: folder,
		}
	}
	return StartupGrantRefusal{}
}

// WritableRoots returns the paths of every rule in policy that carries
// AccessWrite — the set the startup grants (and the exec paths) must never
// overlap.
func WritableRoots(policy SandboxPolicy) []string {
	roots := make([]string, 0, len(policy.FilesystemRules))
	for _, r := range policy.FilesystemRules {
		if r.Access&AccessWrite != 0 {
			roots = append(roots, r.Path)
		}
	}
	return roots
}

// buildStartupGrantRules turns the startup grants into read+execute rules,
// in order, one per distinct path. The access bits are fixed here, as in
// buildExecPathRules: a startup grant is never writable (BR-2.1, BR-2.3).
// A refused entry gets no rule and exactly one warning naming it.
func buildStartupGrantRules(grants []string, writableRoots []string, warnFn func(msg string, path string)) []PathRule {
	if len(grants) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(grants))
	rules := make([]PathRule, 0, len(grants))
	for _, entry := range grants {
		key := entry
		if filepath.IsAbs(entry) {
			key = filepath.Clean(entry)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if refusal := ClassifyStartupGrant(entry, writableRoots); refusal.Refused() {
			if warnFn != nil {
				warnFn(refusal.Reason, key)
			}
			continue
		}
		rules = append(rules, PathRule{Path: key, Access: AccessRead | AccessExecute})
	}
	return rules
}

// firstOverlap returns the first entry of others that path equals, contains
// or lies inside, compared in the space the kernel enforces (declared and
// symlink-resolved forms, letter case ignored — see overlapsAny, which is
// defined by it).
func firstOverlap(path string, others []string) (string, bool) {
	candidates := comparisonForms(path)
	for _, other := range others {
		for _, o := range comparisonForms(other) {
			for _, c := range candidates {
				if pathCoversFold(o, c) || pathCoversFold(c, o) {
					return other, true
				}
			}
		}
	}
	return "", false
}
