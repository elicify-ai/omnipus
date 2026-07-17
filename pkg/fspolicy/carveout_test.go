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
			{filepath.Join(home, "agents", "self", "SOUL.md"), true}, // own home NOT within WorkDir here -> still a carve-out
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
			// A file that is a sibling of the carve-out roots (directly
			// under $OMNIPUS_HOME but not one of the four roots itself) is
			// correctly NOT a carve-out — only the four named roots are.
			{filepath.Join(home, "config.json"), false},
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

func TestBuildCarveOuts_FourFixedRoots(t *testing.T) {
	home := filepath.FromSlash("/omnh")
	got := buildCarveOuts(home)
	want := []string{
		filepath.Join(home, "master.key"),
		filepath.Join(home, "credentials.json"),
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
