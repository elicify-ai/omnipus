//go:build goolm && stdjson

package fspolicy

import (
	"path/filepath"
	"testing"
)

// TestCarveOut_AnchoredOnOmnipusHome_NotWorkingDir is the BLOCK #5
// regression: isCrossAgentPath (pkg/tools/filesystem.go:98-115) derived its
// "agents root" from filepath.Dir(absWorkspace), which is correct only when
// the working dir happens to be exactly agents/<id>/. Under a re-rooted
// workspace turn (WorkDir == workspaces/<id>/work/) that derivation silently
// stopped protecting anything under agents/ at all. IsCarveOut must instead
// anchor exclusively on the boot-known $OMNIPUS_HOME, regardless of what
// WorkDir currently is.
func TestCarveOut_AnchoredOnOmnipusHome_NotWorkingDir(t *testing.T) {
	home := filepath.FromSlash("/omnh")

	t.Run("re-rooted workspace turn: agent's own home is as unreachable as any other's", func(t *testing.T) {
		workDir := filepath.Join(home, "workspaces", "W", "work")
		policy := FSPolicy{
			WorkDir:   workDir,
			Scope:     FSScopeConfined,
			CarveOuts: buildCarveOuts(home),
		}

		cases := []struct {
			path string
			want bool
		}{
			{filepath.Join(home, "agents", "other", "SOUL.md"), true},
			{
				filepath.Join(home, "agents", "self", "SOUL.md"),
				true,
			}, // own home NOT within WorkDir here -> still a carve-out
			{filepath.Join(home, "master.key"), true},
			{filepath.Join(home, "credentials.json"), true},
			{filepath.Join(home, "workspaces", "other", "work", "x"), true},
			{filepath.Join(workDir, "x.txt"), false}, // inside the turn's own WorkDir -> not a carve-out
		}

		for _, tc := range cases {
			if got := IsCarveOut(tc.path, policy); got != tc.want {
				t.Errorf("IsCarveOut(%q) = %v, want %v", tc.path, got, tc.want)
			}
		}
	})

	t.Run("agent-home-rooted turn: own home is not a carve-out of itself, others still are", func(t *testing.T) {
		selfHome := filepath.Join(home, "agents", "self")
		policy := FSPolicy{
			WorkDir:   selfHome,
			Scope:     FSScopeConfined,
			CarveOuts: buildCarveOuts(home),
		}

		cases := []struct {
			path string
			want bool
		}{
			{filepath.Join(selfHome, "SOUL.md"), false}, // within WorkDir -> own-tree exception
			{selfHome, false}, // WorkDir root itself -> own-tree exception
			{filepath.Join(home, "agents", "other", "SOUL.md"), true},
			{filepath.Join(home, "master.key"), true},
			{filepath.Join(home, "credentials.json"), true},
			{filepath.Join(home, "workspaces", "other", "work", "x"), true},
		}

		for _, tc := range cases {
			if got := IsCarveOut(tc.path, policy); got != tc.want {
				t.Errorf("IsCarveOut(%q) = %v, want %v", tc.path, got, tc.want)
			}
		}
	})
}

// TestIsCarveOut_WorkDirAtOmnipusHome_OwnTreeExceptionDoesNotApply is the
// BLOCK #2 regression: before the fix, IsCarveOut's own-tree exception
// checked only "is cleanPath within-or-equal policy.WorkDir", with no
// relationship required between WorkDir and the matched carve-out root. A
// misconfigured (or attacker-influenced) WorkDir == $OMNIPUS_HOME made every
// carve-out root "within WorkDir" simultaneously, so ALL FOUR carve-outs
// (master.key, credentials.json, agents/, workspaces/) classified as
// NOT-a-carve-out. This pins the fix: when WorkDir IS $OMNIPUS_HOME itself
// (or an ancestor of it), the own-tree exception must never fire — every
// carve-out root stays a carve-out.
func TestIsCarveOut_WorkDirAtOmnipusHome_OwnTreeExceptionDoesNotApply(t *testing.T) {
	home := filepath.FromSlash("/omnh")

	t.Run("WorkDir == $OMNIPUS_HOME exempts nothing", func(t *testing.T) {
		policy := FSPolicy{
			WorkDir:   home,
			Scope:     FSScopeUnrestricted,
			CarveOuts: buildCarveOuts(home),
		}

		cases := []struct {
			path string
			want bool
		}{
			{filepath.Join(home, "master.key"), true},
			{filepath.Join(home, "credentials.json"), true},
			{filepath.Join(home, "agents", "self", "SOUL.md"), true},
			{filepath.Join(home, "agents"), true},
			{filepath.Join(home, "workspaces", "W", "work", "x"), true},
			{filepath.Join(home, "workspaces"), true},
			// config.json IS a carve-out as of ADR-063 FR-3.2. It was not
			// before, and that was the hole: a child that can write it sets
			// sandbox.mode: off and removes its own confinement on the next
			// boot. The own-tree exception cannot re-admit it here either —
			// that exception needs WorkDir to be a PROPER DESCENDANT of the
			// root, and WorkDir == $OMNIPUS_HOME is not a descendant of
			// $OMNIPUS_HOME/config.json at all.
			{filepath.Join(home, "config.json"), true},
			{filepath.Join(home, "cli.token"), true},
			// Still NOT a carve-out: an ordinary file that merely sits beside
			// the roots. The set is named entries plus backup prefixes, never
			// "everything directly under $OMNIPUS_HOME".
			{filepath.Join(home, "notes.txt"), false},
		}

		for _, tc := range cases {
			if got := IsCarveOut(tc.path, policy); got != tc.want {
				t.Errorf("IsCarveOut(%q) with WorkDir==home = %v, want %v", tc.path, got, tc.want)
			}
		}
	})

	t.Run("WorkDir an ancestor of $OMNIPUS_HOME exempts nothing", func(t *testing.T) {
		ancestor := filepath.Dir(home)
		policy := FSPolicy{
			WorkDir:   ancestor,
			Scope:     FSScopeUnrestricted,
			CarveOuts: buildCarveOuts(home),
		}

		cases := []string{
			filepath.Join(home, "master.key"),
			filepath.Join(home, "credentials.json"),
			filepath.Join(home, "agents", "self", "SOUL.md"),
			filepath.Join(home, "workspaces", "W", "work", "x"),
		}
		for _, path := range cases {
			if !IsCarveOut(path, policy) {
				t.Errorf("IsCarveOut(%q) with WorkDir==ancestor(home) = false, want true", path)
			}
		}
	})
}

// TestBuildCarveOuts_MergedSecretSet pins the ADR-063 FR-3.2 union.
//
// This was TestBuildCarveOuts_FiveFixedRoots, asserting the app layer's own
// five-entry list. That list is gone: buildCarveOuts now returns
// SecretPaths, the single definition shared with the kernel layer. The two
// used to be maintained separately and had drifted in BOTH directions — the
// app layer denied agents/ and workspaces/ that the kernel granted, while the
// kernel denied config.json and cli.token that the app layer left readable.
//
// The two entries this test gains are the ones that were the live hole:
// config.json (a child rewriting it disables the sandbox) and cli.token (a
// live gateway bearer token, previously readable through read_file by any
// agent running unrestricted).
func TestBuildCarveOuts_MergedSecretSet(t *testing.T) {
	home := filepath.FromSlash("/omnh")
	got := buildCarveOuts(home)
	want := []string{
		filepath.Join(home, "master.key"),
		filepath.Join(home, "credentials.json"),
		filepath.Join(home, "config.json"),
		filepath.Join(home, "cli.token"),
		filepath.Join(home, "entities"),
		// Added after the set was re-derived against a live install rather
		// than carried over from the two old lists. auth.json is plaintext
		// OAuth access + refresh tokens (pkg/auth/store.go); backups/*.tar.gz
		// is an archive of the ENTIRE vault (createTarGz excludes only logs/
		// and backups/); system/ holds the audit log and its HMAC chain
		// anchor, which a child could truncate without ever reading.
		filepath.Join(home, "auth.json"),
		filepath.Join(home, "backups"),
		filepath.Join(home, "system"),
		// ADR-072 D10 Part A / D10.1: skills is path-denied like the entries
		// above (SecretEntriesAlwaysPathOnly), but — unlike them — is
		// deliberately excluded from pkg/tools/shell.go's literal-text guard,
		// because "skills" is an ordinary English word. See
		// TestSecretEntries_SkillsDeniedForPathsNotTextGuard for that half.
		filepath.Join(home, "skills"),
		filepath.Join(home, "agents"),
		filepath.Join(home, "workspaces"),
	}
	if len(got) != len(want) {
		t.Fatalf("buildCarveOuts(%q) = %v, want %v", home, got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("buildCarveOuts(%q)[%d] = %q, want %q", home, i, got[i], w)
		}
	}
}

// TestEntitiesIsCarveOut is the ADR-054 D2/D4 regression: entities/ (where
// pkg/entity persists per-entity records, starting with the AgentConfig
// record split out of config.json) MUST be on the carve-out list under
// BOTH FSScopeConfined and FSScopeUnrestricted. Before this fix,
// buildCarveOuts returned only 4 roots and did not include entities/ at
// all — under FSScopeUnrestricted an agent could rewrite ANY agent's
// AgentConfig record (tool policy, Locked, Default), a strictly worse
// escalation than the agents/-only carve-out this replaces (that at least
// shielded other agents' home directories).
func TestEntitiesIsCarveOut(t *testing.T) {
	home := filepath.FromSlash("/omnh")
	entitiesAgentRecord := filepath.Join(home, "entities", "agents", "some-other-agent.json")
	entitiesRoot := filepath.Join(home, "entities")

	t.Run("FSScopeConfined, WorkDir inside an agent's own home", func(t *testing.T) {
		selfHome := filepath.Join(home, "agents", "self")
		policy := FSPolicy{
			WorkDir:   selfHome,
			Scope:     FSScopeConfined,
			CarveOuts: buildCarveOuts(home),
		}
		if !IsCarveOut(entitiesAgentRecord, policy) {
			t.Errorf("IsCarveOut(%q) under FSScopeConfined = false, want true", entitiesAgentRecord)
		}
		if !IsCarveOut(entitiesRoot, policy) {
			t.Errorf("IsCarveOut(%q) under FSScopeConfined = false, want true", entitiesRoot)
		}
	})

	t.Run("FSScopeUnrestricted, WorkDir inside an agent's own home", func(t *testing.T) {
		selfHome := filepath.Join(home, "agents", "self")
		policy := FSPolicy{
			WorkDir:   selfHome,
			Scope:     FSScopeUnrestricted,
			CarveOuts: buildCarveOuts(home),
		}
		if !IsCarveOut(entitiesAgentRecord, policy) {
			t.Errorf("IsCarveOut(%q) under FSScopeUnrestricted = false, want true — "+
				"an unrestricted agent must still be unable to rewrite another agent's entity record", entitiesAgentRecord)
		}
		if !IsCarveOut(entitiesRoot, policy) {
			t.Errorf("IsCarveOut(%q) under FSScopeUnrestricted = false, want true", entitiesRoot)
		}
	})

	t.Run("FSScopeUnrestricted, WorkDir a re-rooted workspace turn", func(t *testing.T) {
		workDir := filepath.Join(home, "workspaces", "W", "work")
		policy := FSPolicy{
			WorkDir:   workDir,
			Scope:     FSScopeUnrestricted,
			CarveOuts: buildCarveOuts(home),
		}
		if !IsCarveOut(entitiesAgentRecord, policy) {
			t.Errorf("IsCarveOut(%q) under a workspace-turn WorkDir = false, want true", entitiesAgentRecord)
		}
	})

	t.Run("WorkDir == $OMNIPUS_HOME exempts nothing (mirrors the other four roots)", func(t *testing.T) {
		policy := FSPolicy{
			WorkDir:   home,
			Scope:     FSScopeUnrestricted,
			CarveOuts: buildCarveOuts(home),
		}
		if !IsCarveOut(entitiesRoot, policy) {
			t.Errorf("IsCarveOut(%q) with WorkDir==home = false, want true", entitiesRoot)
		}
		if !IsCarveOut(entitiesAgentRecord, policy) {
			t.Errorf("IsCarveOut(%q) with WorkDir==home = false, want true", entitiesAgentRecord)
		}
	})
}

func TestIsWithinOrEqual_TrailingSeparatorGuard(t *testing.T) {
	// "/a/bc" must never be mistaken for a descendant of "/a/b".
	a := filepath.FromSlash("/a/bc")
	root := filepath.FromSlash("/a/b")
	if isWithinOrEqual(a, root) {
		t.Errorf("isWithinOrEqual(%q, %q) = true, want false", a, root)
	}

	child := filepath.FromSlash("/a/b/c")
	if !isWithinOrEqual(child, root) {
		t.Errorf("isWithinOrEqual(%q, %q) = false, want true", child, root)
	}

	if !isWithinOrEqual(root, root) {
		t.Errorf("isWithinOrEqual(%q, %q) = false, want true (equal)", root, root)
	}
}
