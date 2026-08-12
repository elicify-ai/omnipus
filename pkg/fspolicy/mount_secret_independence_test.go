//go:build goolm && stdjson

package fspolicy

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsCarveOut_IgnoresAllowedRoots is the spec FR-7.7 regression: "A test
// MUST prove FR-7.6 rather than assume it: with $HOME mounted, a child MUST
// still be unable to read cli.token or write config.json or master.key...
// if it ever fails, FR-7.6 must revert to refusal."
//
// FR-7.6 (ADR-061 D6, operator decision 2026-08-12: "warn and allow applies
// to all but the omnipus directory") allows mounting a target that CONTAINS
// $OMNIPUS_HOME — $HOME being the common case, since $OMNIPUS_HOME defaults
// to ~/.omnipus. That reasoning rests entirely on the secret set being
// subtracted from every grant independently of where the grant came from —
// this test proves the mechanism that makes that true, rather than trusting
// the reasoning in the spec's prose.
//
// The proof is structural, not behavioral-under-load: IsCarveOut's decision
// (pkg/fspolicy/carveout.go) is a function of policy.CarveOuts and
// policy.WorkDir ONLY — its signature and body never read policy.AllowedRoots
// at all. A mount populates AllowedRoots (FR-6.1); it therefore CANNOT alter
// a single IsCarveOut verdict, structurally, regardless of what host
// directory the mount grants. Populating AllowedRoots with a root that
// contains — or even equals — one of the CarveOuts changes nothing: the
// secret entries were never conditioned on AllowedRoots' contents to begin
// with. This is what makes pkg/tools.ResolvePath's own resolution order safe
// (it checks fspolicy.IsCarveOut UNCONDITIONALLY, before ever consulting
// policy.AllowedRoots for a write — see resolvepath.go's own resolution-order
// comment) — this test verifies the piece of that order this package alone
// is responsible for.
//
// # The layout is REAL on disk, deliberately
//
// This test used a fictional path (/home/operator/.omnipus) that exists on no
// machine. Every stat inside IsCarveOut therefore failed, every decision fell
// through to the string fallback, and the identity leg — the whole primitive
// pathidentity.go exists to install, and the only leg a case or normalization
// payload has to defeat — was never executed once. The test passed while
// exercising the fallback and nothing else. Creating the directories makes the
// identity leg the one under test, which is what FR-7.7 actually depends on.
func TestIsCarveOut_IgnoresAllowedRoots(t *testing.T) {
	base := t.TempDir()
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	// $HOME, mounted per FR-7.6 — it CONTAINS $OMNIPUS_HOME, which is exactly
	// the shape the operator's decision explicitly allows.
	mountedHome := filepath.Join(realBase, "operator")
	home := filepath.Join(mountedHome, ".omnipus")
	workDir := filepath.Join(home, "workspaces", "w1", "work")

	for _, d := range []string{workDir, filepath.Join(home, "entities", "agents")} {
		if mkErr := os.MkdirAll(d, 0o750); mkErr != nil {
			t.Fatalf("mkdir %s: %v", d, mkErr)
		}
	}
	for _, f := range []string{"cli.token", "config.json", "master.key", "credentials.json"} {
		if wErr := os.WriteFile(filepath.Join(home, f), []byte("secret"), 0o600); wErr != nil {
			t.Fatalf("write %s: %v", f, wErr)
		}
	}
	if wErr := os.WriteFile(filepath.Join(home, "entities", "agents", "mia.json"), []byte("{}"), 0o600); wErr != nil {
		t.Fatalf("write entity: %v", wErr)
	}

	policyWithoutMount := FSPolicy{
		WorkDir:      workDir,
		Scope:        FSScopeConfined,
		CarveOuts:    buildCarveOuts(home),
		AllowedRoots: nil,
	}
	policyWithHomeMounted := FSPolicy{
		WorkDir:   workDir,
		Scope:     FSScopeConfined,
		CarveOuts: buildCarveOuts(home),
		// The mount grant (FR-6.1) — deliberately a SUPERSET of every
		// carve-out root, since $HOME contains ~/.omnipus entirely.
		AllowedRoots: []string{mountedHome},
	}

	secretPaths := []string{
		filepath.Join(home, "cli.token"),
		filepath.Join(home, "config.json"),
		filepath.Join(home, "master.key"),
		filepath.Join(home, "credentials.json"),
		filepath.Join(home, "entities", "agents", "mia.json"),
	}

	for _, p := range secretPaths {
		t.Run(p, func(t *testing.T) {
			withoutMount := IsCarveOut(p, policyWithoutMount)
			withMount := IsCarveOut(p, policyWithHomeMounted)

			if !withoutMount {
				t.Fatalf("IsCarveOut(%q) without any mount = false, want true (test setup bug — this path must be a carve-out to begin with)", p)
			}
			if !withMount {
				t.Fatalf(
					"FR-7.7 VIOLATED: IsCarveOut(%q) = false once AllowedRoots contains a mount of $HOME (%q), which CONTAINS $OMNIPUS_HOME (%q). "+
						"FR-7.6's warn-and-allow decision rests entirely on this staying true — if this assertion ever fails, FR-7.6 must revert to refusal.",
					p, mountedHome, home,
				)
			}
		})
	}
}

// TestDeniedPathsFor_IgnoresAllowedRoots is the DeniedPathsFor-side sibling
// of TestIsCarveOut_IgnoresAllowedRoots — the kernel rendering
// (sandbox.DeriveKernelPolicy) sources its per-turn denied set from
// fspolicy.DeniedPathsFor(home, workDir), which — like IsCarveOut — takes no
// AllowedRoots parameter at all and cannot be influenced by one.
func TestDeniedPathsFor_IgnoresAllowedRoots(t *testing.T) {
	home := filepath.FromSlash("/home/operator/.omnipus")
	workDir := filepath.Join(home, "workspaces", "w1", "work")

	// DeniedPathsFor's signature has no AllowedRoots parameter — there is
	// nothing a mount could pass in even if it wanted to. This test exists
	// so a future signature change that DID add one would have to touch
	// this exact assertion, rather than silently reopening the FR-7.7 gap.
	denied := DeniedPathsFor(home, workDir)

	want := map[string]bool{
		filepath.Join(home, "cli.token"):        false,
		filepath.Join(home, "config.json"):      false,
		filepath.Join(home, "master.key"):       false,
		filepath.Join(home, "credentials.json"): false,
	}
	for _, d := range denied {
		if _, ok := want[d]; ok {
			want[d] = true
		}
	}
	for p, found := range want {
		if !found {
			t.Fatalf("DeniedPathsFor(%q, %q) = %v, want it to include %q", home, workDir, denied, p)
		}
	}
}
