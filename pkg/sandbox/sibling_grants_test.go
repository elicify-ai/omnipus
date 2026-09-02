// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
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

	got, err := ExpandRulesExcluding(rules, SecretPaths(home), nil, nil)
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
	// instead of protecting it. "skills" is deliberately NOT here any more —
	// ADR-072 D10 Part A / D10.1 added it to fspolicy.SecretEntriesAlwaysPathOnly,
	// so it is now one of the secrets excluded above, the same as "entities".
	for _, sibling := range []string{"agents", "sessions", "logs"} {
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
// ADR-062 §4.0 turns on, on the Linux side too. Getting these two backwards
// breaks every agent's working directory while looking like hardening.
func TestExpandRulesExcluding_AgentsStayWritableEntitiesDoNot(t *testing.T) {
	home := seedHome(t)
	got, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite | AccessExecute}},
		SecretPaths(home), nil, nil)
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
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite}}, denied, nil, nil)
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
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite}}, denied, nil, nil)
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
	t.Cleanup(func() {
		// Restore writable mode so t.TempDir cleanup can remove it.
		if chmodErr := os.Chmod(home, 0o700); chmodErr != nil {
			_ = chmodErr
		}
	})

	_, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead}}, SecretPaths(home), nil, nil)
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

	// "Unrelated" means the rule contains no secret. A root that HAPPENS to
	// contain home is not unrelated, and expanding it is the correct
	// behaviour — withholding the secret is the whole point of this function.
	//
	// t.TempDir() honours $TMPDIR, so home lands under /tmp on a stock Linux
	// box (GitHub runners) but under /var/folders on macOS and under
	// /cache/tmp on the Fly CI worker, which sets TMPDIR explicitly. Hardcoding
	// /tmp as "unrelated" therefore passed on two of those three environments
	// and failed on the one where the kernel sandbox actually runs. Filter by
	// the real relationship instead of assuming it.
	candidates := []string{"/tmp", "/usr/lib"}
	unrelated := make([]string, 0, len(candidates))
	rules := []PathRule{{Path: home, Access: AccessRead}}
	for _, c := range candidates {
		if isAtOrUnderAny(home, []string{c}) {
			continue // home lives inside it — genuinely related, must expand
		}
		unrelated = append(unrelated, c)
		rules = append(rules, PathRule{Path: c, Access: AccessRead | AccessWrite})
	}
	// Positive lower bound: if $TMPDIR ever makes every candidate an ancestor
	// of home, this test would assert nothing at all.
	if len(unrelated) == 0 {
		t.Fatalf("no unrelated root survived filtering against home %q — the test would be vacuous", home)
	}

	got, err := ExpandRulesExcluding(rules, SecretPaths(home), nil, nil)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	paths := grantedPaths(t, got)
	for _, want := range unrelated {
		if !contains(paths, want) {
			t.Errorf("unrelated rule %q must pass through unchanged; got %v", want, paths)
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
		[]PathRule{{Path: inside, Access: AccessRead | AccessWrite}}, SecretPaths(home), nil, nil)
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
	got, err := ExpandRulesExcluding(rules, nil, nil, nil)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/tmp" {
		t.Errorf("expected identity, got %v", got)
	}
}

// TestExpandRulesExcluding_TopLevelCreationIsNarrowed pins a narrowing that
// follows from sibling-granting: the home DIRECTORY ITSELF is no longer
// granted, so on Linux a child can no longer create a new entry at the top
// level of $OMNIPUS_HOME.
//
// This was first written up as a risk to watch. It is not one — it moves the
// kernel closer to the policy the product already intends, and the intent is
// checkable rather than a matter of opinion:
//
//   - agents default to RestrictToWorkspace: true (pkg/config/defaults.go), so
//     a turn is confined to its own working directory;
//   - pkg/fspolicy/carveout.go denies agents/, workspaces/, entities/,
//     master.key and credentials.json outright, at the application layer,
//     regardless of scope.
//
// So writing at the top level of $OMNIPUS_HOME was never something an agent was
// supposed to do. The kernel was simply broader than the policy above it, and
// this closes part of that gap.
//
// The two lists still disagree in BOTH directions, which is worth knowing and
// is not fixed here: the app layer denies agents/ and workspaces/ that the
// kernel grants, and the kernel denies config.json and cli.token that the app
// layer does not. An agent running unrestricted can still reach the gateway
// bearer token through an in-process file tool, because the kernel sandbox
// covers CHILD processes and those tools are not children.
//
// The second-order effect: a top-level entry created after a child spawned is
// unreachable by that child until the next spawn re-enumerates (see
// TestExpandRulesExcluding_EnumeratesAtCallTime). Neither effect applies to
// macOS, where the home grant stays whole and secrets are removed by deny.
func TestExpandRulesExcluding_TopLevelCreationIsNarrowed(t *testing.T) {
	home := seedHome(t)
	got, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite | AccessExecute}},
		SecretPaths(home), nil, nil)
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
	// it. "skills" is deliberately NOT here any more — ADR-072 D10 Part A /
	// D10.1 made it a secret (fspolicy.SecretEntriesAlwaysPathOnly), so it must
	// NOT be granted; see the explicit check for that below.
	for _, sub := range []string{"agents", "sessions"} {
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

	skillsPath := filepath.Join(home, "skills")
	for _, r := range got {
		if r.Path == skillsPath {
			t.Errorf("%q must NOT be granted — it is a secret (ADR-072 D10 Part A / D10.1)", skillsPath)
		}
	}
}

// TestExpandRulesExcluding_NeverGrantsADeniedNode is the LINUX half of the
// directory-node fix, and the reason the Linux backend needs no new rule.
//
// macOS closes the rename bypass with an explicit
// (deny file-write* (literal $OMNIPUS_HOME/agents)). Landlock has no deny
// primitive, so the equivalent claim there is structural: the grant-based walk
// must never hand the node a write right in the first place. That is asserted
// here rather than asserted in prose, because "Linux needs nothing" is exactly
// the kind of claim that is true when written and quietly false after an edit
// to siblingGrants.
//
// The mechanism: siblingGrants puts every directory on the path to a denied
// entry into `traverse`, enumerates it, and SKIPS any child that is itself
// traversed — so <home>/agents is enumerated (to grant <home>/agents/self) and
// is never itself emitted as a rule. A future change that granted the
// traversed directory wholesale would reintroduce the rename bypass on Linux
// while every macOS test stayed green.
func TestExpandRulesExcluding_NeverGrantsADeniedNode(t *testing.T) {
	home := seedHome(t)
	for _, d := range []string{
		filepath.Join("agents", "self"),
		filepath.Join("agents", "victim"),
		filepath.Join("workspaces", "w1", "work"),
		filepath.Join("workspaces", "w2", "work"),
	} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	for _, shape := range []struct {
		name    string
		workDir string
	}{
		{"agent-home-rooted", filepath.Join(home, "agents", "self")},
		{"re-rooted workspace turn", filepath.Join(home, "workspaces", "w1", "work")},
	} {
		t.Run(shape.name, func(t *testing.T) {
			policy := DeriveKernelPolicy(
				fspolicy.FSPolicy{WorkDir: shape.workDir, Scope: fspolicy.FSScopeConfined},
				TurnPolicyInput{HomePath: home, Model: FilesystemModelOpen},
			)
			if len(policy.DeniedNodes) == 0 {
				t.Fatalf("DeriveKernelPolicy produced no DeniedNodes for %s; the node list is "+
					"unwired again and the macOS rename bypass is back", shape.workDir)
			}

			expanded, err := ExpandRulesExcluding(policy.FilesystemRules, policy.DeniedPaths, policy.DeniedNodes, policy.DeniedPathPrefixes)
			if err != nil {
				t.Fatalf("expand: %v", err)
			}

			for _, node := range policy.DeniedNodes {
				for _, r := range expanded {
					if r.Access&AccessWrite == 0 {
						continue
					}
					if pathIsUnder(node, r.Path) {
						t.Errorf("Landlock would grant WRITE covering the denied node %q via rule %q "+
							"(access=%d). A write right on the node is a rename right on it, which "+
							"relocates every other agent's/workspace's tree out from under the deny list.",
							node, r.Path, r.Access)
					}
				}
			}

			// Control: the work dir itself must still be granted for write, or
			// the assertion above is satisfied by a policy that grants nothing.
			var workGranted bool
			for _, r := range expanded {
				if r.Access&AccessWrite != 0 && pathIsUnder(shape.workDir, r.Path) {
					workGranted = true
					break
				}
			}
			if !workGranted {
				t.Errorf("the work dir %q carries no write grant; the node exclusion must not cost "+
					"the agent its own tree", shape.workDir)
			}
		})
	}
}
