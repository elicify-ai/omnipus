// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-092 D7/FR-012 — Level 1 anti-drift lock test.
//
// The single-source-of-truth guarantee D7 exists to make true: for a fixed
// fspolicy.FSPolicy (INCLUDING a non-empty PathGrants), the D7 pre-flight
// evaluator (tools.EvaluateFSPreflight) and sandbox.DeriveKernelPolicy's
// ACTUAL rendered SandboxPolicy must agree, path by path and access class by
// access class, on whether the command is contained or needs an escalation.
// Pure Go, runs on every CI OS — the Linux-only second leg (a real
// Landlock-confined child) lives in fspreflight_antidrift_linux_test.go and
// cannot run here or on macOS/Windows; see that file's header.
package sandbox_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// kernelRulesGrant is the ORACLE this test compares tools.EvaluateFSPreflight
// against. It does NOT re-implement containment by hand — it calls the SAME
// production function the Linux backend calls to turn a blanket $OMNIPUS_HOME
// grant plus a deny list into the concrete rule set a real child receives
// (sandbox.ExpandRulesExcluding, sibling_grants.go), then asks whether the
// EXPANDED rule set covers path under Landlock/Seatbelt's own hierarchical
// grant semantics (a rule on a directory covers everything beneath it).
//
// Using the real expansion function, rather than a hand-rolled
// "under some FilesystemRules entry, minus DeniedPaths" check, matters: a
// naive re-implementation of containment is exactly the kind of SECOND
// derivation D7's own "one source of truth" principle forbids, and it
// produces a wrong answer for a non-existent sibling path (e.g. a
// never-created agents/<other>/ directory) — ExpandRulesExcluding enumerates
// EXISTING children only, so a not-yet-created path under a denied-NODE
// directory gets no rule at all (unreachable, matching IsCarveOut's refusal),
// not "covered by the parent's blanket grant" the way a same-package
// re-implementation without disk enumeration would wrongly conclude.
func kernelRulesGrant(t *testing.T, policy sandbox.SandboxPolicy, path string, access uint64) bool {
	t.Helper()
	expanded, err := sandbox.ExpandRulesExcluding(
		policy.FilesystemRules, policy.DeniedPaths, policy.DeniedNodes, policy.DeniedPathPrefixes)
	if err != nil {
		t.Fatalf("ExpandRulesExcluding: %v", err)
	}
	clean := filepath.Clean(path)
	var have uint64
	for _, r := range expanded {
		rp := filepath.Clean(r.Path)
		if clean == rp || pathIsUnderForTest(clean, rp) {
			have |= r.Access
		}
	}
	return have&access == access
}

func pathIsUnderForTest(child, parent string) bool {
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

// verdictAgreesWithKernel is the comparator FR-012 requires: a refused
// verdict must correspond to NO kernel grant; a contained verdict must
// correspond to an ACTUAL kernel grant; an escalation verdict (neither
// refused nor contained) makes no claim about the pre-widening kernel
// rendering, because it is by definition the case the pre-flight has not
// yet resolved (the whole point of asking first).
func verdictAgreesWithKernel(v tools.FSPreflightVerdict, kernelGrants bool) bool {
	if v.Refused {
		return !kernelGrants
	}
	if v.Contained {
		return kernelGrants
	}
	return true
}

// TestAntiDrift_Comparator_CatchesDeliberateMismatch proves
// verdictAgreesWithKernel actually discriminates agreement from
// disagreement — FR-012's own requirement ("a deliberately mismatched pair
// must fail the test") — rather than being a function that always returns
// true. Each sub-case below is the shape a REAL regression would take.
func TestAntiDrift_Comparator_CatchesDeliberateMismatch(t *testing.T) {
	cases := []struct {
		name    string
		verdict tools.FSPreflightVerdict
		kernel  bool
		want    bool
	}{
		{
			name:    "refused verdict paired with an actual kernel grant disagrees (FR-037 regression shape)",
			verdict: tools.FSPreflightVerdict{Refused: true},
			kernel:  true,
			want:    false,
		},
		{
			name:    "contained verdict paired with no actual kernel grant disagrees (over-eager pre-flight)",
			verdict: tools.FSPreflightVerdict{Contained: true},
			kernel:  false,
			want:    false,
		},
		{
			name:    "refused verdict paired with no kernel grant agrees",
			verdict: tools.FSPreflightVerdict{Refused: true},
			kernel:  false,
			want:    true,
		},
		{
			name:    "contained verdict paired with an actual kernel grant agrees",
			verdict: tools.FSPreflightVerdict{Contained: true},
			kernel:  true,
			want:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := verdictAgreesWithKernel(tc.verdict, tc.kernel)
			if got != tc.want {
				t.Fatalf("verdictAgreesWithKernel(%+v, %v) = %v, want %v — comparator failed to catch the deliberate mismatch",
					tc.verdict, tc.kernel, got, tc.want)
			}
		})
	}
}

// fixedTestPolicy builds one realistic FSPolicy — WorkDir, CarveOuts (via
// the real EffectiveFSPolicy computation, not hand-rolled), an AllowedRoots
// mount, and a non-empty PathGrants — that the whole Level 1 matrix below
// evaluates against, matching FR-012's "a fixed FSPolicy including a
// non-empty PathGrants" requirement.
func fixedTestPolicy(t *testing.T) (home string, policy fspolicy.FSPolicy, allowedRoot, grantPath string) {
	t.Helper()
	rawHome := t.TempDir()
	workDir := filepath.Join(rawHome, "agents", "self")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}
	// Another agent's home, pre-created as an EXISTING sibling directory —
	// the real KernelDeniedPathsFor mechanism enumerates disk to find
	// siblings (its own doc comment), so a not-yet-created sibling would
	// never appear in the deny list at all (unreachable for lack of any
	// rule, not "explicitly denied") and would be a weaker adversarial case
	// than a real install, where other agents' homes already exist.
	if err := os.MkdirAll(filepath.Join(rawHome, "agents", "victim"), 0o755); err != nil {
		t.Fatalf("mkdir victim home: %v", err)
	}
	allowedRoot = filepath.Join(rawHome, "mount1")
	if err := os.MkdirAll(allowedRoot, 0o755); err != nil {
		t.Fatalf("mkdir allowedRoot: %v", err)
	}
	grantDir := filepath.Join(rawHome, "granted")
	if err := os.MkdirAll(grantDir, 0o755); err != nil {
		t.Fatalf("mkdir grantDir: %v", err)
	}
	grantPath = filepath.Join(grantDir, "widened.txt")

	base, err := fspolicy.EffectiveFSPolicy(context.Background(), workDir, "", true, rawHome, "self", "")
	if err != nil {
		t.Fatalf("EffectiveFSPolicy: %v", err)
	}
	base.AllowedRoots = []string{allowedRoot}
	base.PathGrants = []fspolicy.PathGrant{
		{Path: grantPath, Access: fspolicy.PathGrantAccessRead | fspolicy.PathGrantAccessWrite},
	}
	if err = base.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// home is returned REALPATH'd (EvalSymlinks), matching what
	// EffectiveFSPolicy resolved base.WorkDir/base.CarveOuts against — on
	// macOS /var is a symlink to /private/var, so t.TempDir()'s raw path and
	// the policy's own resolved paths are lexically different strings for
	// the SAME directory. Callers that build a secret-set or carve-out path
	// from the returned home (e.g. filepath.Join(home, "master.key")) get a
	// string IsCarveOut's identity check actually resolves, rather than one
	// that only matches by accident on a platform with no such symlink.
	resolvedHome, err := filepath.EvalSymlinks(rawHome)
	if err != nil {
		t.Fatalf("EvalSymlinks(rawHome): %v", err)
	}
	return resolvedHome, base, allowedRoot, grantPath
}

// TestAntiDrift_Level1_PathGrantMatrix is FR-012's Level 1 lock test: for
// every {path, access} pair below, tools.EvaluateFSPreflight's verdict and
// sandbox.DeriveKernelPolicy's actual rendering must agree, via
// verdictAgreesWithKernel.
//
// Scoped to WRITE-bearing access classes (Write, Read|Write) rather than a
// full read-write cross product: for a non-ReadConfined agent, a read-only
// PathOperation is ALWAYS Contained without consulting WorkDir/AllowedRoots/
// PathGrants at all (ADR-063 D2 — reads are open outside the secret set),
// and the kernel's own read posture for that case is governed by
// SandboxPolicy.ReadsOpen (a FilesystemModel-wide flag), not by an
// enumerable PathRule — a different, FilesystemModel-scoped comparison this
// matrix does not need in order to prove D7/FR-036's actual subject: does a
// PathGrant (or WorkDir/AllowedRoots) reach the kernel exactly as granted.
func TestAntiDrift_Level1_PathGrantMatrix(t *testing.T) {
	home, policy, allowedRoot, grantPath := fixedTestPolicy(t)

	kernel := sandbox.DeriveKernelPolicy(policy, sandbox.TurnPolicyInput{
		HomePath: home,
		Model:    sandbox.FilesystemModelConfined,
	})

	writeAccess := fspolicy.PathGrantAccessWrite
	readWriteAccess := fspolicy.PathGrantAccessRead | fspolicy.PathGrantAccessWrite

	cases := []struct {
		name   string
		path   string
		access uint64
	}{
		{"WorkDir write is contained", filepath.Join(policy.WorkDir, "f.txt"), writeAccess},
		{"AllowedRoots write is contained", filepath.Join(allowedRoot, "f.txt"), writeAccess},
		{"PathGrants exact path, write, is contained", grantPath, writeAccess},
		{"PathGrants exact path, read+write (within granted access), is contained", grantPath, readWriteAccess},
		{"outside path, no grant, write escalates", filepath.Join(home, "outside", "f.txt"), writeAccess},
		{"master.key write is refused, never escalated", filepath.Join(home, "master.key"), writeAccess},
		{"credentials.json write is refused, never escalated", filepath.Join(home, "credentials.json"), writeAccess},
		{"another agent's home write is refused (cross-agent carve-out)", filepath.Join(home, "agents", "victim", "SOUL.md"), writeAccess},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict := tools.EvaluateFSPreflight(policy, tc.path, tc.access, false)
			kernelGrants := kernelRulesGrant(t, kernel, tc.path, tc.access)
			if !verdictAgreesWithKernel(verdict, kernelGrants) {
				t.Fatalf("DRIFT: pre-flight verdict %+v disagrees with kernel rendering (kernelGrants=%v) for path %q access %d",
					verdict, kernelGrants, tc.path, tc.access)
			}
		})
	}
}

// TestAntiDrift_SecretSet_NeverWidenable is the mandatory adversarial case
// (C-5/FR-037): a PathGrant naming a path in the secret set can never open
// it, at either layer.
//
//  1. fspolicy.FSPolicy.Validate refuses to construct the policy at all —
//     the pre-flight's own decision to refuse, at construction time.
//  2. Even bypassing Validate (constructing the struct literal directly,
//     modeling a hypothetical upstream bug that skipped validation),
//     sandbox.DeriveKernelPolicy's own defense-in-depth guard
//     (pathGrantIsDenied) drops the grant rather than rendering it — proven
//     here by checking the ACTUAL rendered SandboxPolicy grants nothing at
//     the secret-set path, not by trusting the guard exists.
func TestAntiDrift_SecretSet_NeverWidenable(t *testing.T) {
	home, policy, _, _ := fixedTestPolicy(t)
	secretPath := filepath.Join(home, "master.key")

	t.Run("Validate refuses construction", func(t *testing.T) {
		bad := policy
		bad.PathGrants = append(append([]fspolicy.PathGrant{}, policy.PathGrants...),
			fspolicy.PathGrant{Path: secretPath, Access: fspolicy.PathGrantAccessRead | fspolicy.PathGrantAccessWrite})
		if err := bad.Validate(); err == nil {
			t.Fatal("Validate accepted a PathGrant naming the secret set — FR-037 requires refusal")
		}
	})

	t.Run("DeriveKernelPolicy never renders it even bypassing Validate", func(t *testing.T) {
		bypassed := policy
		bypassed.PathGrants = []fspolicy.PathGrant{
			{Path: secretPath, Access: fspolicy.PathGrantAccessRead | fspolicy.PathGrantAccessWrite},
		}
		kernel := sandbox.DeriveKernelPolicy(bypassed, sandbox.TurnPolicyInput{
			HomePath: home,
			Model:    sandbox.FilesystemModelConfined,
		})
		if kernelRulesGrant(t, kernel, secretPath, fspolicy.PathGrantAccessWrite) {
			t.Fatal("DeriveKernelPolicy rendered a PathGrant landing on master.key — the secret set was reopened")
		}
		for _, r := range kernel.FilesystemRules {
			if filepath.Clean(r.Path) == filepath.Clean(secretPath) {
				t.Fatalf("DeriveKernelPolicy emitted a PathRule exactly at the secret path: %+v", r)
			}
		}
	})
}
