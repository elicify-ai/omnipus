// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

func granted(policy SandboxPolicy, path string) (PathRule, bool) {
	want := filepath.Clean(path)
	for _, r := range policy.FilesystemRules {
		if filepath.Clean(r.Path) == want {
			return r, true
		}
	}
	return PathRule{}, false
}

func denied(policy SandboxPolicy, path string) bool {
	want := filepath.Clean(path)
	for _, p := range policy.DeniedPaths {
		if filepath.Clean(p) == want {
			return true
		}
	}
	return false
}

// TestDeriveKernelPolicy_IsTotal is spec FR-1.4, and it is the guard against
// the exact failure that produced the divergence this change repairs: a field
// added to the authored policy that one layer honours and the other silently
// ignores.
//
// For every authored field with a kernel expression, changing that field MUST
// change the derived policy. A field that can be changed with no effect is
// either dead or forgotten, and both are bugs — the second one silently.
func TestDeriveKernelPolicy_IsTotal(t *testing.T) {
	home := t.TempDir()
	base := fspolicy.FSPolicy{WorkDir: filepath.Join(home, "workspaces", "w1", "work")}
	in := TurnPolicyInput{HomePath: home, Model: FilesystemModelOpen}

	reference := DeriveKernelPolicy(base, in)

	for _, tc := range []struct {
		field  string
		mutate func(*fspolicy.FSPolicy, *TurnPolicyInput)
		why    string
	}{
		{
			field:  "WorkDir",
			mutate: func(p *fspolicy.FSPolicy, _ *TurnPolicyInput) { p.WorkDir = filepath.Join(home, "agents", "self") },
			why:    "the work dir is the turn's write grant and drives the own-tree exception",
		},
		{
			field:  "AllowedRoots",
			mutate: func(p *fspolicy.FSPolicy, _ *TurnPolicyInput) { p.AllowedRoots = []string{"/srv/mounted"} },
			why:    "mounts are write grants; ignoring them makes every mount inert at the kernel layer",
		},
		{
			field:  "Model",
			mutate: func(_ *fspolicy.FSPolicy, i *TurnPolicyInput) { i.Model = FilesystemModelConfined },
			why:    "the filesystem model decides whether reads and execution are open at all",
		},
		{
			field:  "HomePath",
			mutate: func(_ *fspolicy.FSPolicy, i *TurnPolicyInput) { i.HomePath = t.TempDir() },
			why:    "the secret set is anchored to $OMNIPUS_HOME",
		},
		{
			field:  "AllowedPaths",
			mutate: func(_ *fspolicy.FSPolicy, i *TurnPolicyInput) { i.AllowedPaths = []string{"/srv/extra"} },
			why:    "operator-configured write grants must reach the kernel",
		},
		{
			field:  "BindPorts",
			mutate: func(_ *fspolicy.FSPolicy, i *TurnPolicyInput) { i.BindPorts = []uint16{5173} },
			why:    "a dev server that cannot bind its port cannot serve",
		},
	} {
		t.Run(tc.field, func(t *testing.T) {
			p, i := base, in
			tc.mutate(&p, &i)
			got := DeriveKernelPolicy(p, i)
			if reflect.DeepEqual(got, reference) {
				t.Errorf("changing %s produced an IDENTICAL kernel policy — the field is "+
					"either dead or silently ignored by the kernel rendering.\nwhy it matters: %s",
					tc.field, tc.why)
			}
		})
	}
}

// TestDeriveKernelPolicy_WorkDirAndMountsAreWriteGrants covers ADR-063 D4: a
// mount grants write, because reads need no grant under the open model.
func TestDeriveKernelPolicy_WorkDirAndMountsAreWriteGrants(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "workspaces", "w1", "work")
	authored := fspolicy.FSPolicy{WorkDir: work, AllowedRoots: []string{"/srv/repo"}}

	policy := DeriveKernelPolicy(authored, TurnPolicyInput{HomePath: home, Model: FilesystemModelOpen})

	for _, want := range []string{work, "/srv/repo"} {
		rule, ok := granted(policy, want)
		if !ok {
			t.Fatalf("%q must be granted; it is the turn's writable area", want)
		}
		if rule.Access&AccessWrite == 0 {
			t.Errorf("%q must carry WRITE — a mount that is not writable is "+
				"indistinguishable from no mount, since reads are already open", want)
		}
	}
}

// TestDeriveKernelPolicy_OwnTreeExceptionReachesTheKernel is the assertion that
// the app layer's per-root exception actually survives the derivation.
//
// Without it the kernel would be MORE permissive than the app layer on every
// workspace turn — the divergence this change exists to remove, reintroduced by
// the very function meant to prevent it.
func TestDeriveKernelPolicy_OwnTreeExceptionReachesTheKernel(t *testing.T) {
	home := t.TempDir()
	in := TurnPolicyInput{HomePath: home, Model: FilesystemModelOpen}

	agentTurn := DeriveKernelPolicy(
		fspolicy.FSPolicy{WorkDir: filepath.Join(home, "agents", "self")}, in)
	if denied(agentTurn, filepath.Join(home, "agents")) {
		t.Error("agent-home-rooted turn: agents/ must be re-admitted, or the agent cannot " +
			"reach the directory it is working in")
	}

	workspaceTurn := DeriveKernelPolicy(
		fspolicy.FSPolicy{WorkDir: filepath.Join(home, "workspaces", "w1", "work")}, in)
	if !denied(workspaceTurn, filepath.Join(home, "agents")) {
		t.Error("re-rooted workspace turn: agents/ must stay denied at the KERNEL layer too — " +
			"the app layer denies it here, and a kernel that does not is the divergence " +
			"this whole change removes")
	}

	// The context-free entries hold in every shape.
	for name, p := range map[string]SandboxPolicy{"agent-rooted": agentTurn, "workspace-rooted": workspaceTurn} {
		for _, secret := range []string{"master.key", "cli.token", "config.json"} {
			if !denied(p, filepath.Join(home, secret)) {
				t.Errorf("%s turn: %s must be denied in every shape", name, secret)
			}
		}
	}
}

// TestDeriveKernelPolicy_ScopeIsNotAReadRestriction pins spec FR-2.5 at the
// derivation boundary. Scope governs WRITES now; rendering it as a read
// restriction here would put the kernel back out of step with the app layer.
func TestDeriveKernelPolicy_ScopeIsNotAReadRestriction(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "workspaces", "w1", "work")
	in := TurnPolicyInput{HomePath: home, Model: FilesystemModelOpen}

	confined := DeriveKernelPolicy(fspolicy.FSPolicy{WorkDir: work, Scope: fspolicy.FSScopeConfined}, in)
	unrestricted := DeriveKernelPolicy(fspolicy.FSPolicy{WorkDir: work, Scope: fspolicy.FSScopeUnrestricted}, in)

	if !confined.ReadsOpen || !unrestricted.ReadsOpen {
		t.Error("reads must be open under both scopes — the model decides reads, not the scope")
	}
	if !reflect.DeepEqual(confined.FilesystemRules, unrestricted.FilesystemRules) {
		t.Error("Scope must not change the kernel's filesystem rules: it governs writes " +
			"through the work dir and mounts, which are the same in both cases here")
	}
}

// TestDeriveKernelPolicy_MountInsideWorkDirIsNotDuplicated: harmless to
// Landlock, which unions rights per path, but a duplicate rule is noise in a
// rendered Seatbelt profile that a reader then has to discount.
func TestDeriveKernelPolicy_MountInsideWorkDirIsNotDuplicated(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "workspaces", "w1", "work")
	authored := fspolicy.FSPolicy{WorkDir: work, AllowedRoots: []string{work}}

	policy := DeriveKernelPolicy(authored, TurnPolicyInput{HomePath: home, Model: FilesystemModelOpen})

	var count int
	for _, r := range policy.FilesystemRules {
		if filepath.Clean(r.Path) == filepath.Clean(work) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("work dir emitted %d times, want 1", count)
	}
}
