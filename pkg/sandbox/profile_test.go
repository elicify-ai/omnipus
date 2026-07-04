package sandbox_test

import (
	"os"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// containsPath is a helper to check that a path string contains the given
// substring (case-sensitive, OS path separator agnostic).
func containsPath(path, substr string) bool {
	return strings.Contains(path, substr)
}

// TestBuildLimits verifies the fixed Limits construction that replaced the
// old per-agent kernel-profile switch (ADR-035-remove-per-agent-sandbox-profile).
func TestBuildLimits(t *testing.T) {
	t.Parallel()

	const timeout = int32(30)

	t.Run("normal call returns expected cwd/limits/proxy-addr", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		proxy, err := sandbox.NewEgressProxy([]string{"registry.npmjs.org"}, nil)
		if err != nil {
			t.Fatalf("NewEgressProxy: %v", err)
		}
		defer proxy.Close()

		lim, err := sandbox.BuildLimits(dir, proxy, timeout)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lim.WorkspaceDir == "" {
			t.Errorf("expected WorkspaceDir to be set")
		}
		if lim.EgressProxyAddr == "" {
			t.Errorf("expected non-empty EgressProxyAddr when proxy is non-nil")
		}
		if lim.EgressProxyAddr != proxy.Addr() {
			t.Errorf("EgressProxyAddr = %q; want %q", lim.EgressProxyAddr, proxy.Addr())
		}
		if lim.TimeoutSeconds != timeout {
			t.Errorf("expected TimeoutSeconds=%d, got %d", timeout, lim.TimeoutSeconds)
		}
	})

	t.Run("nil proxy yields empty proxy addr", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		lim, err := sandbox.BuildLimits(dir, nil, timeout)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lim.EgressProxyAddr != "" {
			t.Errorf("expected empty EgressProxyAddr for nil proxy, got %q", lim.EgressProxyAddr)
		}
		if lim.WorkspaceDir == "" {
			t.Errorf("expected WorkspaceDir to be set")
		}
	})

	t.Run("empty workspaceDir returns error", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.BuildLimits("", nil, timeout)
		if err == nil {
			t.Errorf("expected error for empty workspaceDir")
		}
	})

	t.Run("non-existent workspaceDir is created", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		nested := base + "/does-not-exist-yet"
		lim, err := sandbox.BuildLimits(nested, nil, timeout)
		if err != nil {
			t.Fatalf("unexpected error for non-existent workspaceDir: %v", err)
		}
		if lim.WorkspaceDir == "" {
			t.Errorf("expected non-empty WorkspaceDir for non-existent path, got empty string")
		}
		if !containsPath(lim.WorkspaceDir, "does-not-exist-yet") {
			t.Errorf("WorkspaceDir %q does not appear to be the abs-pathed input", lim.WorkspaceDir)
		}
	})
}

// TestResolveLimits verifies the god-mode bypass added to close the
// workspace_shell / workspace_shell_bg asymmetry (Fix 1 of the 7-reviewer
// SandboxProfile-removal gate): godMode=true must return the zero Limits
// without touching the filesystem at all — no MkdirAll, no proxy lookup —
// while godMode=false must delegate to BuildLimits exactly as before.
func TestResolveLimits(t *testing.T) {
	t.Parallel()

	const timeout = int32(30)

	t.Run("godMode=true returns zero Limits and does not create the workspace dir", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		// A nested, not-yet-existing path: if ResolveLimits fell through to
		// BuildLimits under god mode, resolveWorkspaceDir would MkdirAll this
		// into existence — the side effect this test proves does NOT happen.
		nested := base + "/does-not-exist-under-god-mode"

		proxy, err := sandbox.NewEgressProxy([]string{"registry.npmjs.org"}, nil)
		if err != nil {
			t.Fatalf("NewEgressProxy: %v", err)
		}
		defer proxy.Close()

		lim, err := sandbox.ResolveLimits(true, nested, proxy, timeout)
		if err != nil {
			t.Fatalf("unexpected error under god mode: %v", err)
		}
		if lim != (sandbox.Limits{}) {
			t.Errorf("expected zero Limits under god mode, got %+v", lim)
		}
		if _, statErr := os.Stat(nested); !os.IsNotExist(statErr) {
			t.Errorf("god mode must not create the workspace dir; os.Stat(%q) error = %v", nested, statErr)
		}
	})

	t.Run("godMode=true with empty workspaceDir returns zero Limits, no error", func(t *testing.T) {
		t.Parallel()
		// BuildLimits("", ...) alone returns an error (empty workspaceDir).
		// ResolveLimits must short-circuit before that check is ever reached.
		lim, err := sandbox.ResolveLimits(true, "", nil, timeout)
		if err != nil {
			t.Fatalf("expected no error for empty workspaceDir under god mode, got: %v", err)
		}
		if lim != (sandbox.Limits{}) {
			t.Errorf("expected zero Limits under god mode, got %+v", lim)
		}
	})

	t.Run("godMode=false delegates to BuildLimits exactly", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		proxy, err := sandbox.NewEgressProxy([]string{"registry.npmjs.org"}, nil)
		if err != nil {
			t.Fatalf("NewEgressProxy: %v", err)
		}
		defer proxy.Close()

		want, wantErr := sandbox.BuildLimits(dir, proxy, timeout)
		if wantErr != nil {
			t.Fatalf("BuildLimits: %v", wantErr)
		}
		got, gotErr := sandbox.ResolveLimits(false, dir, proxy, timeout)
		if gotErr != nil {
			t.Fatalf("ResolveLimits(godMode=false): %v", gotErr)
		}
		if got != want {
			t.Errorf("ResolveLimits(godMode=false) = %+v; want BuildLimits result %+v", got, want)
		}
	})

	t.Run("godMode=false with empty workspaceDir returns the same error as BuildLimits", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.ResolveLimits(false, "", nil, timeout)
		if err == nil {
			t.Errorf("expected error for empty workspaceDir when godMode=false")
		}
	})
}
