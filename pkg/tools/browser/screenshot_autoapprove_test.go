// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestScreenshotTool_AutoApproveVerdict_Inside is ADR-092 D9's browser
// "inside" case (§3.5): a JPEG that resolves inside the agent's own work
// dir runs under Auto. Expected values (Run=true, Class="runs_if_args", one
// pinned write path under the work dir) come from
// docs/internal/specs/adr-092-auto-for-other-tools-design.md §3.5/§5.1, not
// from AutoApproveVerdict's own source.
func TestScreenshotTool_AutoApproveVerdict_Inside(t *testing.T) {
	home := t.TempDir()
	agentHome := filepath.Join(home, "agents", "self")
	if err := os.MkdirAll(agentHome, 0o755); err != nil {
		t.Fatalf("mkdir agent home: %v", err)
	}
	t.Setenv(config.EnvHome, home)
	resolvedAgentHome, err := filepath.EvalSymlinks(agentHome)
	if err != nil {
		t.Fatalf("resolve agent home: %v", err)
	}

	tool := &ScreenshotTool{agentHome: agentHome, restrict: true}
	verdict := tool.AutoApproveVerdict(context.Background(), nil)

	if !verdict.Run {
		t.Fatalf("want Run=true for a destination inside the agent's own work dir, got %+v", verdict)
	}
	if verdict.Class != "runs_if_args" {
		t.Errorf("Class = %q, want runs_if_args", verdict.Class)
	}
	if len(verdict.Paths) != 1 {
		t.Fatalf("want exactly one pinned path, got %d (%+v)", len(verdict.Paths), verdict.Paths)
	}
	pinned := verdict.Paths[0]
	if filepath.Dir(pinned.Real) != resolvedAgentHome {
		t.Errorf("pinned path %q is not directly inside the work dir %q", pinned.Real, resolvedAgentHome)
	}
	if pinned.Access != tools.AutoAccessWrite {
		t.Errorf("Access = %d, want AutoAccessWrite (%d)", pinned.Access, tools.AutoAccessWrite)
	}
	// The classify-time preview must never touch disk: it only checks
	// whether the write WOULD be permitted.
	entries, err := os.ReadDir(agentHome)
	if err != nil {
		t.Fatalf("read agent home: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("AutoApproveVerdict must not write anything, found %d entries in %q", len(entries), agentHome)
	}
}

// TestScreenshotTool_AutoApproveVerdict_Outside is ADR-092 D9's browser
// "outside" case: browser_screenshot exposes no filename argument (its
// Parameters() schema has no properties, so nothing a caller supplies can
// steer the destination) — its ONLY way to fail the §2 J2 workspace rule is
// for the destination to be unresolvable at all: no agent home and no
// per-turn Workspace re-root, so ResolveTurnFSPolicy has no working
// directory to resolve (fspolicy's explicit "no working directory"
// refusal). The classifier must ask, never run, on that failure — §2
// condition 4: "The classifier ran without error... any error counts as
// asks."
func TestScreenshotTool_AutoApproveVerdict_Outside(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	tool := &ScreenshotTool{agentHome: "", restrict: true}
	verdict := tool.AutoApproveVerdict(context.Background(), nil)

	if verdict.Run {
		t.Fatalf("want Run=false when the destination cannot be resolved at all, got %+v", verdict)
	}
	if verdict.Class != "asks" {
		t.Errorf("Class = %q, want asks", verdict.Class)
	}
	if verdict.Reason == "" {
		t.Error("want a non-empty Reason explaining the refusal")
	}
	if len(verdict.Paths) != 0 {
		t.Errorf("an asks verdict must carry no pinned paths, got %v", verdict.Paths)
	}
}

// TestScreenshotTool_WriteTimeRecheck_RefusesWhenDestinationMovedOutside is
// ADR-092 D9 §5.3's "re-check at write time that refuses if it moved
// outside", applied to browser_screenshot: AutoApproveVerdict and Execute
// share resolveScreenshotDestination, so Execute's own resolution IS the
// re-check — a second, independent call made with whatever the environment
// looks like right now, not what AutoApproveVerdict saw earlier. This test
// proves the two calls are independent: the destination resolves at
// classify time, the environment changes (the agent's own work dir is
// removed — the same observable failure a swapped-to-nothing symlink would
// produce), and the second resolution — the one Execute performs
// immediately before every write — must refuse.
func TestScreenshotTool_WriteTimeRecheck_RefusesWhenDestinationMovedOutside(t *testing.T) {
	home := t.TempDir()
	agentHome := filepath.Join(home, "agents", "self")
	if err := os.MkdirAll(agentHome, 0o755); err != nil {
		t.Fatalf("mkdir agent home: %v", err)
	}
	t.Setenv(config.EnvHome, home)

	tool := &ScreenshotTool{agentHome: agentHome, restrict: true}

	verdict := tool.AutoApproveVerdict(context.Background(), nil)
	if !verdict.Run {
		t.Fatalf("setup: want Run=true before the environment changes, got %+v", verdict)
	}

	// The environment changes between classify time and the real write.
	if err := os.RemoveAll(agentHome); err != nil {
		t.Fatalf("remove agent home: %v", err)
	}

	handle, err := tool.resolveScreenshotDestination(context.Background(), screenshotFilename())
	if err == nil {
		_ = handle.Close()
		t.Fatal("want the write-time re-check to refuse once the destination no longer resolves, got a handle")
	}
}

// TestScreenshotTool_AutoApproveVerdict_ImplementsClassifier is a
// compile-time-ish guard that fails loudly if ScreenshotTool stops
// satisfying tools.AutoApproveClassifier.
func TestScreenshotTool_AutoApproveVerdict_ImplementsClassifier(t *testing.T) {
	var _ tools.AutoApproveClassifier = (*ScreenshotTool)(nil)
}
