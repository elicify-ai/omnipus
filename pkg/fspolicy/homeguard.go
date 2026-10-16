// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"os"
	"path/filepath"
	"strings"
)

// This file evaluates the #1284 home guard (amendment §2 D1/D4/D5):
//
//	protected(path) = inInventory(path)                       -- named, wins
//	                OR ( under($OMNIPUS_HOME, path) AND NOT allowed(path) )
//	allowed(path)   = within a HomeGuard.Allow entry OR within the turn's
//	                  WorkDir (own-tree exception, narrowed by D4)
//
// It is the ONE predicate leg behind IsCarveOut, so ResolvePath, grep's
// carveOutFS, the bash command check and the D7 pre-flight all inherit it
// through that single function. A zero HomeGuard (Home == "") disables the
// leg entirely — the pre-#1284 behaviour every hand-constructed policy keeps.

// homeGuardDeny reports whether the home guard denies cleanPath (already
// filepath.Clean'd by the caller). Identity-based containment throughout
// (CoversForDeny / CoversForGrant), so case, Unicode-normalisation and
// symlink-spelling aliases of the same location cannot split the answer —
// pathidentity.go's header records the measured leaks a byte comparison
// allowed.
func homeGuardDeny(cleanPath string, policy FSPolicy) bool {
	home := policy.HomeGuard.Home
	if home == "" {
		return false
	}
	if !CoversForDeny(home, cleanPath) {
		// Outside the home: the guard has no opinion (D3 A9 — ordinary
		// external paths are unchanged).
		return false
	}

	// The named inventory always wins — over allow entries, over mounts,
	// over the own-tree exception (after D4's narrowing, below).
	if inventoryDenies(home, cleanPath, policy) {
		return true
	}

	// Default-deny: anything under the home that no explicit allowance and
	// no own-tree exception re-admits is protected, whether or not it exists
	// yet (D1: a forgotten future directory is safe by construction).
	return !allowedUnderHome(cleanPath, policy)
}

// inventoryDenies reports whether cleanPath matches the named protected
// inventory (amendment §2 D2) or one of its prefix rules, or the D4
// agent-home-internal set.
func inventoryDenies(home, cleanPath string, policy FSPolicy) bool {
	for _, name := range ProtectedInventoryNames {
		if CoversForDeny(filepath.Join(home, name), cleanPath) {
			return true
		}
	}

	// boot_epoch* — prefix-matched directly in $OMNIPUS_HOME (a minted file
	// may not exist yet; the prefix covers it by construction). Secret-backup
	// prefixes (config.json.* and friends) are checked earlier in IsCarveOut
	// by CoversSecretBackup and are deliberately not repeated here.
	if dir := filepath.Dir(cleanPath); SameLocationForDeny(dir, home) &&
		hasProtectedInventoryPrefix(filepath.Base(cleanPath)) {
		return true
	}

	// A7 (amendment §2 D3): the skills Allow entry re-admits bundled HELPER
	// files only — a skill's instruction leaf stays gated at the predicate
	// level, so no consumer of this one predicate (resolver, grep, command
	// check, pre-flight) can read it. The resolvepath gate keeps its own
	// redirect message on top; the names are single-sourced in
	// SkillInstructionLeaves. The registry shelf is $OMNIPUS_HOME/skills;
	// skills under an agent home are D4-protected wholesale below.
	if CoversForDeny(filepath.Join(home, "skills"), cleanPath) &&
		IsSkillInstructionLeaf(filepath.Base(cleanPath)) {
		return true
	}

	return agentHomeInternalDeny(cleanPath, policy)
}

// allowedUnderHome reports whether an explicit allowance re-admits cleanPath:
// a HomeGuard.Allow entry (media, skill helper files, the turn's own upload
// dirs, workspace mounts) or the turn's WorkDir. Inventory matches never
// reach here — inventoryDenies has already answered.
func allowedUnderHome(cleanPath string, policy FSPolicy) bool {
	for _, entry := range policy.HomeGuard.Allow {
		if entry.Path == "" {
			continue
		}
		if CoversForGrant(filepath.Clean(entry.Path), cleanPath) {
			return true
		}
	}
	if policy.WorkDir != "" && CoversForGrant(filepath.Clean(policy.WorkDir), cleanPath) {
		return true
	}
	// A8 setup roots are validated workspace work folders, not broad home
	// allowances. Keep the inventory-first decision above them, and do not
	// let a hand-constructed malformed root bypass that boundary.
	for _, root := range policy.SetupRoots {
		cleanRoot := filepath.Clean(root)
		if isWorkspaceWorkDir(cleanRoot, policy.CarveOuts) && CoversForGrant(cleanRoot, cleanPath) {
			return true
		}
	}
	return false
}

// agentHomeInternalDeny applies the D4 narrowing: for an agent-home-rooted
// turn (WorkDir == <home>/agents/<id> or below it), the own-tree exception
// stays for the agent home as a working area, but these entries inside it
// remain protected: .omnipus/**, memory/, sessions/, skills/, SOUL.md,
// AGENT.md, HEARTBEAT.md and MEMORY.md.
//
// Shape-matched by NAME under the caller's own agent home, and only there:
// during a workspace-rooted turn the default-deny leg already denies every
// agent's home, so restricting the rule to agent-home-rooted turns costs
// nothing and never fires inside a workspace work tree (an agent-created
// directory that happens to be named "agents/x/skills" stays reachable).
//
// Deny-side: the name comparison folds case, so a spelled-up "SOUL.md" alias
// of the definition file is still refused; over-matching a distinctly-named
// file inside the agent's own internal directories withholds only an access
// the agent has no legitimate claim to.
func agentHomeInternalDeny(cleanPath string, policy FSPolicy) bool {
	work := filepath.Clean(policy.WorkDir)
	if work == "" || !isAgentHomeRoot(policy.HomeGuard.Home, work) {
		return false
	}
	// Only the caller's OWN tree can reach this rule — anything else under
	// the home is already denied by the default-deny leg, and the rule's one
	// job is to stop the own-tree exception from re-admitting the internal
	// entries (D4).
	if !CoversForGrant(work, cleanPath) {
		return false
	}
	rel, err := filepath.Rel(work, cleanPath)
	if err != nil {
		// Unrelatable to the own tree: deny (fail closed).
		return true
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return false // the work dir itself stays operable
	}
	head := strings.ToLower(strings.SplitN(rel, "/", 2)[0])
	for _, name := range AgentHomeInternalFiles {
		if head == strings.ToLower(name) {
			return true
		}
	}
	for _, name := range AgentHomeInternalDirs {
		if head == strings.ToLower(name) {
			return true
		}
	}
	return false
}

// VisibleDirectoryEntries omits protected child paths from a directory listing.
// Use the resolved directory and its turn policy before pagination, so neither
// names nor page totals disclose protected entries. Ordinary files with the
// same basenames elsewhere remain visible because IsCarveOut is path-based.
func VisibleDirectoryEntries(dir string, entries []os.DirEntry, policy FSPolicy) []os.DirEntry {
	visible := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !IsCarveOut(filepath.Join(dir, entry.Name()), policy) {
			visible = append(visible, entry)
		}
	}
	return visible
}

// isAgentHomeRoot reports whether workDir is an agent home — <home>/agents/<id>
// or a directory strictly below one. Identity-based on the home coverage and
// shape-based on the agents/ component, which is exactly the granularity the
// D4 rule needs.
func isAgentHomeRoot(home, workDir string) bool {
	if home == "" || !CoversForDeny(home, workDir) {
		return false
	}
	rel, err := filepath.Rel(home, workDir)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	return len(parts) >= 2 && strings.EqualFold(parts[0], "agents") && parts[1] != ""
}

// IsProtectedHomePath is the turn-free, conservative form of the home guard:
// everything under home is protected except the base read-only allow entries
// (media), the workspace work trees (workspaces/<id>/work/**, by shape), and
// the workspace records'/internal trees' explicit answers. No session-scoped
// upload allowance and no mount knowledge — a caller with a turn must use
// IsCarveOut with a real FSPolicy instead. Used by consumers that run before
// or without a turn policy: the no-sandbox read-only allowlist gate and the
// bash command guard's fail-closed fallback when the turn policy cannot be
// resolved.
//
// Skills helper files are deliberately NOT re-admitted here: this predicate
// speaks for spawned children and unclassifiable commands, and children keep
// the whole-directory kernel deny on skills (ADR-072 D10.3).
func IsProtectedHomePath(rawPath, home string) bool {
	if home == "" {
		return false
	}
	clean := filepath.Clean(rawPath)
	if !CoversForDeny(home, clean) {
		return false
	}

	// Backup copies and boot_epoch minted files, directly in $OMNIPUS_HOME.
	if dir := filepath.Dir(clean); SameLocationForDeny(dir, home) {
		base := filepath.Base(clean)
		if hasSecretBackupPrefix(base) || hasProtectedInventoryPrefix(base) {
			return true
		}
	}

	// Named inventory.
	for _, name := range ProtectedInventoryNames {
		if CoversForDeny(filepath.Join(home, name), clean) {
			return true
		}
	}

	// Workspace shape, without a turn: the work tree of a workspace the
	// gateway actually knows (its record file exists) is the one part of a
	// workspace an agent legitimately reaches; the record files, the
	// internal .omnipus directory, other workspaces' trees and every other
	// workspace entry stay protected. The record-existence guard is what
	// keeps a foreign or orphaned workspace's work tree denied here — with a
	// real turn, IsCarveOut answers from the turn's actual WorkDir instead
	// and this shape leg never runs.
	rel, err := filepath.Rel(home, clean)
	if err != nil {
		return true
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) >= 3 && strings.EqualFold(parts[0], "workspaces") {
		if strings.EqualFold(parts[2], workspaceWorkDirName) {
			return !workspaceRecordExists(home, parts[1])
		}
		if strings.EqualFold(parts[2], workspaceInternalDir) {
			return true
		}
	}
	if len(parts) == 2 && strings.EqualFold(parts[0], "workspaces") &&
		strings.HasSuffix(strings.ToLower(parts[1]), ".json") {
		return true // the workspace record file
	}

	// Base read-only allow entries (A3 media) stay reachable; A7 skills stays
	// protected (see this function's doc comment).
	for _, name := range BaseAllowRelative {
		if strings.EqualFold(name, "skills") {
			continue
		}
		if CoversForGrant(filepath.Join(home, name), clean) {
			return false
		}
	}
	return true
}

// workspaceRecordExists reports whether the workspace record file
// workspaces/<id>.json exists under home. Existence only — the record is
// NOT parsed — because this predicate runs without a turn and may only
// narrow a deny when the gateway demonstrably knows the workspace; an
// unreadable record still shows the workspace is managed.
func workspaceRecordExists(home, wsID string) bool {
	if wsID == "" || strings.ContainsAny(wsID, "/\\") || wsID == "." || wsID == ".." {
		return false
	}
	_, err := os.Stat(filepath.Join(home, "workspaces", wsID+".json"))
	return err == nil
}

// InventoryGuarded reports whether a resolved path is held by the #1284
// STRUCTURAL protection against operator-grant reopening (fix round 1): the
// named protected inventory and its prefixes, the D4 agent-home-internal set,
// the workspace internal directory shape, and the uploads tree outside the
// turn's explicit session-scoped allowances (amendment §2 D2 names
// "uploads/ minus allow entry A4"). Operator pattern adapters consult this
// before turning a matched location into a home-guard allowance: a pattern
// may open an ordinary non-internal home location (D3 A2) but never one of
// these — named inventory always wins.
func InventoryGuarded(policy FSPolicy, path string) bool {
	home := policy.HomeGuard.Home
	if home == "" {
		return false
	}
	clean := filepath.Clean(path)
	if inventoryDenies(home, clean, policy) {
		return true
	}
	// uploads: D2-named minus A4. Inside the acting/root session's own
	// uploads dir the turn's existing allowance already answers; anywhere
	// else under uploads/ an operator pattern must not reach.
	if CoversForDeny(filepath.Join(home, "uploads"), clean) {
		for _, entry := range policy.HomeGuard.Allow {
			if entry.Path != "" && CoversForGrant(filepath.Clean(entry.Path), clean) {
				return false
			}
		}
		return true
	}
	// workspaces/<id>/.omnipus/** by shape.
	rel, err := filepath.Rel(home, clean)
	if err != nil {
		return true
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) >= 3 && strings.EqualFold(parts[0], "workspaces") &&
		strings.EqualFold(parts[2], workspaceInternalDir) {
		return true
	}
	return false
}

// WorkTreeRootFor widens path to its containing workspace work tree —
// <home>/workspaces/<id>/work — when path lies inside one by shape, and
// returns path unchanged otherwise. The God Mode floor uses this so its
// enumeration keeps the turn's WHOLE work tree open (read and write) while a
// deeper per-turn branch such as an environment_setup installation prefix
// would otherwise leave the work tree's own files on the denied side of the
// walk — the exact gap the D7 probe (workspace read from a setup child)
// exercises.
func WorkTreeRootFor(home, path string) string {
	if home == "" || path == "" {
		return path
	}
	cleanHome, cleanPath := filepath.Clean(home), filepath.Clean(path)
	if !CoversForDeny(cleanHome, cleanPath) {
		return path
	}
	rel, err := filepath.Rel(cleanHome, cleanPath)
	if err != nil {
		return path
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) >= 3 && strings.EqualFold(parts[0], "workspaces") && parts[1] != "" {
		return filepath.Join(cleanHome, "workspaces", parts[1], workspaceWorkDirName)
	}
	return path
}
