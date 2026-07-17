//go:build goolm && stdjson

package fspolicy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEffectiveFSPolicy_ReturnsConsistentPolicyShape(t *testing.T) {
	home := t.TempDir()
	agentHome := filepath.Join(home, "agents", "mia")
	if err := os.MkdirAll(agentHome, 0o755); err != nil {
		t.Fatalf("mkdir agentHome: %v", err)
	}
	workDir := filepath.Join(home, "workspaces", "w1", "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}

	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("EvalSymlinks(home): %v", err)
	}
	realWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(workDir): %v", err)
	}
	realAgentHome, err := filepath.EvalSymlinks(agentHome)
	if err != nil {
		t.Fatalf("EvalSymlinks(agentHome): %v", err)
	}

	cases := []struct {
		name        string
		turnWorkDir string
		restrict    bool
		wantScope   FSScope
		wantWorkDir string
	}{
		{"turn work dir set + confined", workDir, true, FSScopeConfined, realWorkDir},
		{"turn work dir set + unrestricted", workDir, false, FSScopeUnrestricted, realWorkDir},
		{"empty turn work dir falls back to agent home", "", true, FSScopeConfined, realAgentHome},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := EffectiveFSPolicy(context.Background(), agentHome, tc.turnWorkDir, tc.restrict, home, "agent-mia", "workspace-w1")
			if err != nil {
				t.Fatalf("EffectiveFSPolicy: %v", err)
			}

			if policy.WorkDir != tc.wantWorkDir {
				t.Errorf("WorkDir = %q, want %q", policy.WorkDir, tc.wantWorkDir)
			}
			if policy.Scope != tc.wantScope {
				t.Errorf("Scope = %q, want %q", policy.Scope, tc.wantScope)
			}

			wantCarveOuts := []string{
				filepath.Join(realHome, "master.key"),
				filepath.Join(realHome, "credentials.json"),
				filepath.Join(realHome, "agents"),
				filepath.Join(realHome, "workspaces"),
			}
			if len(policy.CarveOuts) != len(wantCarveOuts) {
				t.Fatalf("CarveOuts = %v, want %v", policy.CarveOuts, wantCarveOuts)
			}
			for i, want := range wantCarveOuts {
				if policy.CarveOuts[i] != want {
					t.Errorf("CarveOuts[%d] = %q, want %q", i, policy.CarveOuts[i], want)
				}
			}

			if policy.AllowedRoots != nil {
				t.Errorf("AllowedRoots = %v, want nil (P1 seam, P2 populates this)", policy.AllowedRoots)
			}
		})
	}
}

func TestEffectiveFSPolicy_FailsClosedOnUnresolvable(t *testing.T) {
	home := t.TempDir()
	// An embedded NUL byte makes the path invalid at the syscall boundary
	// (Go's BytePtrFromString rejects it) — EvalSymlinks fails with an error
	// that is NOT os.IsNotExist, so realpath must surface it rather than
	// silently falling back to an unresolved path.
	badPath := filepath.Join(home, "bad\x00path")

	t.Run("omnipusHome unresolvable", func(t *testing.T) {
		policy, err := EffectiveFSPolicy(context.Background(), home, home, true, badPath, "a", "w")
		if err == nil {
			t.Fatalf("expected error for unresolvable omnipusHome, got policy %+v", policy)
		}
	})

	t.Run("agent home fallback unresolvable", func(t *testing.T) {
		policy, err := EffectiveFSPolicy(context.Background(), badPath, "", true, home, "a", "w")
		if err == nil {
			t.Fatalf("expected error for unresolvable agent home, got policy %+v", policy)
		}
	})

	t.Run("turn work dir unresolvable", func(t *testing.T) {
		policy, err := EffectiveFSPolicy(context.Background(), home, badPath, true, home, "a", "w")
		if err == nil {
			t.Fatalf("expected error for unresolvable turn work dir, got policy %+v", policy)
		}
	})
}

// TestFSScope_P1NeverReturnsReservedValues asserts that no input combination
// available to P1 (restrict true/false, turnWorkDir present/empty) ever
// yields FSScopeAsk or FSScopeAllow — those are reserved for P2/P3 and P1
// must never invent them (Constraint #6).
func TestFSScope_P1NeverReturnsReservedValues(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "agents", "mia")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for _, restrict := range []bool{true, false} {
		for _, turnWorkDir := range []string{"", dir} {
			policy, err := EffectiveFSPolicy(context.Background(), dir, turnWorkDir, restrict, home, "some-agent", "some-workspace")
			if err != nil {
				t.Fatalf("EffectiveFSPolicy(restrict=%v, turnWorkDir=%q): %v", restrict, turnWorkDir, err)
			}
			if policy.Scope == FSScopeAsk || policy.Scope == FSScopeAllow {
				t.Fatalf("P1 policy returned reserved scope %q for restrict=%v turnWorkDir=%q", policy.Scope, restrict, turnWorkDir)
			}
			if policy.Scope != FSScopeConfined && policy.Scope != FSScopeUnrestricted {
				t.Fatalf("unexpected scope %q for restrict=%v turnWorkDir=%q", policy.Scope, restrict, turnWorkDir)
			}
		}
	}
}
