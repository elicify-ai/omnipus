// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// seedHome builds a realistic $OMNIPUS_HOME: the five secret entries plus the
// ordinary directories an install actually has.
func seedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, d := range []string{"agents", "sessions", "skills", "logs", "entities"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	for _, f := range []string{"master.key", "credentials.json", "config.json", "cli.token"} {
		if err := os.WriteFile(filepath.Join(home, f), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return home
}

func grantedPaths(t *testing.T, rules []PathRule) []string {
	t.Helper()
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Path)
	}
	sort.Strings(out)
	return out
}

func contains(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

// TestExpandRulesExcluding_GrantsSiblingsNotTheSecret is the core of FR-4.5:
// on Linux exclusion means never granting, because there is no deny primitive.
func TestExpandRulesExcluding_GrantsSiblingsNotTheSecret(t *testing.T) {
	home := seedHome(t)
	rules := []PathRule{{Path: home, Access: AccessRead | AccessWrite | AccessExecute}}

	got, err := ExpandRulesExcluding(rules, SecretPaths(home))
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	paths := grantedPaths(t, got)

	if contains(paths, home) {
		t.Fatal("the home root must NOT survive as a whole-tree grant — " +
			"it contains every secret, so granting it defeats the entire exclusion")
	}
	for _, secret := range SecretPaths(home) {
		if contains(paths, secret) {
			t.Errorf("secret %q was granted", secret)
		}
	}
	// The siblings must survive, or the exclusion has simply broken the product
	// instead of protecting it.
	for _, sibling := range []string{"agents", "sessions", "skills", "logs"} {
		want := filepath.Join(home, sibling)
		if !contains(paths, want) {
			t.Errorf("sibling %q must still be granted; got %v", want, paths)
		}
	}

	// Access rights are carried over untouched. This function decides
	// reachability; changing permission here would be a silent policy change.
	for _, r := range got {
		if r.Access != (AccessRead | AccessWrite | AccessExecute) {
			t.Errorf("rule %q access = %#x, want the original RWX", r.Path, r.Access)
		}
	}
}

// TestExpandRulesExcluding_AgentsStayWritableEntitiesDoNot pins the distinction
// ADR-060 §4.0 turns on, on the Linux side too. Getting these two backwards
// breaks every agent's working directory while looking like hardening.
func TestExpandRulesExcluding_AgentsStayWritableEntitiesDoNot(t *testing.T) {
	home := seedHome(t)
	got, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite | AccessExecute}},
		SecretPaths(home))
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	paths := grantedPaths(t, got)

	if !contains(paths, filepath.Join(home, "agents")) {
		t.Error("agents/ must stay granted — it holds agent workspaces")
	}
	if contains(paths, filepath.Join(home, "entities")) {
		t.Error("entities/ must NOT be granted — it holds per-agent tool policy, " +
			"and a child that can write it can flip its own tools to allow")
	}
}

// TestExpandRulesExcluding_EnumeratesAtCallTime is FR-4.5d, and it is the
// reason enumeration happens per spawn rather than once at boot. A directory
// created after the gateway started must be reachable by a child spawned after
// it, or agents would silently lose access to their own new work.
func TestExpandRulesExcluding_EnumeratesAtCallTime(t *testing.T) {
	home := seedHome(t)
	denied := SecretPaths(home)

	before, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite}}, denied)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	late := filepath.Join(home, "created-after-boot")
	if contains(grantedPaths(t, before), late) {
		t.Fatal("precondition: the directory must not exist yet")
	}

	if mkErr := os.MkdirAll(late, 0o700); mkErr != nil {
		t.Fatalf("mkdir: %v", mkErr)
	}
	after, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite}}, denied)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if !contains(grantedPaths(t, after), late) {
		t.Error("a directory created after the first expansion must be granted by the next one — " +
			"otherwise the enumeration is stale and agents lose access to new work")
	}
	// Re-check the secrets: a late-enumeration bug that re-granted the root
	// would also pass the assertion above.
	for _, secret := range denied {
		if contains(grantedPaths(t, after), secret) {
			t.Errorf("secret %q leaked into the re-enumerated grant", secret)
		}
	}
}

// TestExpandRulesExcluding_UnreadableDirectoryFailsClosed is FR-4.5c. The
// tempting fallback is to grant the parent and carry on; that exposes the
// secret while every log line still reads green.
func TestExpandRulesExcluding_UnreadableDirectoryFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions, so the failure cannot be provoked")
	}
	home := seedHome(t)
	if err := os.Chmod(home, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })

	_, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead}}, SecretPaths(home))
	if err == nil {
		t.Fatal("an unreadable directory MUST fail the expansion; " +
			"falling back to granting the parent exposes the secret set silently")
	}
}

// TestExpandRulesExcluding_UnrelatedRulesUntouched: only trees that actually
// contain a secret are rewritten. /tmp, /usr and the rest must pass through
// as single grants, or the rule count explodes and so does spawn cost.
func TestExpandRulesExcluding_UnrelatedRulesUntouched(t *testing.T) {
	home := seedHome(t)
	rules := []PathRule{
		{Path: "/tmp", Access: AccessRead | AccessWrite},
		{Path: "/usr/lib", Access: AccessRead},
		{Path: home, Access: AccessRead},
	}
	got, err := ExpandRulesExcluding(rules, SecretPaths(home))
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	paths := grantedPaths(t, got)
	for _, want := range []string{"/tmp", "/usr/lib"} {
		if !contains(paths, want) {
			t.Errorf("unrelated rule %q must pass through unchanged", want)
		}
	}
}

// TestExpandRulesExcluding_DropsRulesInsideASecret: an operator allowed_path
// pointing into entities/ must not reopen it by the side door.
func TestExpandRulesExcluding_DropsRulesInsideASecret(t *testing.T) {
	home := seedHome(t)
	inside := filepath.Join(home, "entities", "agents")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := ExpandRulesExcluding(
		[]PathRule{{Path: inside, Access: AccessRead | AccessWrite}}, SecretPaths(home))
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a rule inside a denied tree must be dropped, got %v", grantedPaths(t, got))
	}
}

// TestExpandRulesExcluding_NoDeniedPathsIsIdentity keeps the confined path free
// of surprises when no exclusion applies.
func TestExpandRulesExcluding_NoDeniedPathsIsIdentity(t *testing.T) {
	rules := []PathRule{{Path: "/tmp", Access: AccessRead}}
	got, err := ExpandRulesExcluding(rules, nil)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/tmp" {
		t.Errorf("expected identity, got %v", got)
	}
}

// TestExpandRulesExcluding_TopLevelCreationIsNarrowed documents a REAL
// behavioural narrowing that follows from sibling-granting, so it is a known
// accepted consequence rather than something discovered later as a bug.
//
// Replacing the whole-tree $OMNIPUS_HOME grant with per-entry grants means the
// home DIRECTORY ITSELF is no longer granted. Two things follow on Linux:
//
//  1. A child can no longer create a NEW entry at the top level of
//     $OMNIPUS_HOME. Everything inside an already-granted subdirectory
//     (agents/, sessions/, skills/) still works normally, which is where all
//     agent writes actually go — the gateway creates the top-level layout, not
//     its children.
//  2. A top-level entry created AFTER a child was spawned is not reachable by
//     that child. The next spawn re-enumerates and picks it up (see
//     TestExpandRulesExcluding_EnumeratesAtCallTime), so the window is one
//     process lifetime.
//
// Neither applies to macOS, where the home grant stays whole and the secrets
// are removed by explicit deny instead.
func TestExpandRulesExcluding_TopLevelCreationIsNarrowed(t *testing.T) {
	home := seedHome(t)
	got, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite | AccessExecute}},
		SecretPaths(home))
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	paths := grantedPaths(t, got)

	if contains(paths, home) {
		t.Fatal("home root granted whole — the exclusion is not in effect")
	}
	// The case that actually matters: writes INSIDE a granted subdirectory are
	// untouched. If this ever fails, agents cannot write to their own
	// workspaces and the exclusion has broken the product rather than hardened
	// it.
	for _, sub := range []string{"agents", "sessions", "skills"} {
		want := filepath.Join(home, sub)
		var found bool
		for _, r := range got {
			if r.Path == want {
				found = true
				if r.Access&AccessWrite == 0 {
					t.Errorf("%q must remain writable; agents write their work here", want)
				}
			}
		}
		if !found {
			t.Errorf("%q must still be granted", want)
		}
	}
}
