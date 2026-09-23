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
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestScreenshotTool_AutoApproveVerdict_Inside is ADR-092 D9's browser
// "inside" case (§3.5): a JPEG that resolves inside the agent's own work
// dir runs under Auto. Expected values (Run=true,
// Class=AutoVerdictClassRunsIfArgs, one pinned write path under the work
// dir) come from
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
	if verdict.Class != tools.AutoVerdictClassRunsIfArgs {
		t.Errorf("Class = %q, want %q", verdict.Class, tools.AutoVerdictClassRunsIfArgs)
	}
	if len(verdict.Paths) != 1 {
		t.Fatalf("want exactly one pinned path, got %d (%+v)", len(verdict.Paths), verdict.Paths)
	}
	pinned := verdict.Paths[0]
	if filepath.Dir(pinned.Real) != resolvedAgentHome {
		t.Errorf("pinned path %q is not directly inside the work dir %q", pinned.Real, resolvedAgentHome)
	}
	if pinned.Access != fspolicy.PathGrantAccessWrite {
		t.Errorf("Access = %d, want PathGrantAccessWrite (%d)", pinned.Access, fspolicy.PathGrantAccessWrite)
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
	if verdict.Class != tools.AutoVerdictClassAsks {
		t.Errorf("Class = %q, want %q", verdict.Class, tools.AutoVerdictClassAsks)
	}
	if verdict.Reason == "" {
		t.Error("want a non-empty Reason explaining the refusal")
	}
	if len(verdict.Paths) != 0 {
		t.Errorf("an asks verdict must carry no pinned paths, got %v", verdict.Paths)
	}
}

// TestScreenshotTool_WriteScreenshot_RefusesWhenDestinationMovedOutside is
// ADR-092 D9 §5.3's "re-check at write time that refuses if it moved
// outside", exercised through the resolution half of writeScreenshot:
// AutoApproveVerdict and writeScreenshot both resolve through
// ResolveTurnFSPolicy + ResolvePath(FSOpWrite), independently. The
// destination resolves at classify time; the environment then changes (the
// agent's own work dir is removed — the same observable failure a
// swapped-to-nothing symlink would produce); writeScreenshot's own, later
// resolution must refuse, and nothing is written.
func TestScreenshotTool_WriteScreenshot_RefusesWhenDestinationMovedOutside(t *testing.T) {
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

	path, failure := tool.writeScreenshot(context.Background(), []byte("jpeg bytes"))
	if failure == nil {
		t.Fatalf("want writeScreenshot to refuse once the destination no longer resolves, got a write to %q", path)
	}
	if !failure.IsError {
		t.Errorf("want an error result, got %+v", failure)
	}
}

// TestScreenshotTool_WriteScreenshot_RecheckRefusesNarrowerPin proves
// writeScreenshot is actually WIRED to ADR-092 D9 §5.3's re-check
// (tools.RecheckAutoPin), not merely relying on a fresh resolution
// happening to agree with itself. browser_screenshot's destination is
// always a bare, tool-generated relative filename with no caller-supplied
// path component, so ResolvePath alone can never observe it having "moved
// outside" within a single call — the write-time defense unique to the
// pinning mechanism is that a call pinned for a NARROWER access than it is
// about to use gets refused. Here the pin on ctx covers only read access
// for this tool; writeScreenshot needs write access to save the JPEG, so
// the re-check must refuse it and nothing is written — proving the call
// exists and passes fspolicy.PathGrantAccessWrite. Deleting the
// RecheckAutoPin call from writeScreenshot would make this test start
// writing the file and fail.
func TestScreenshotTool_WriteScreenshot_RecheckRefusesNarrowerPin(t *testing.T) {
	home := t.TempDir()
	agentHome := filepath.Join(home, "agents", "self")
	if err := os.MkdirAll(agentHome, 0o755); err != nil {
		t.Fatalf("mkdir agent home: %v", err)
	}
	t.Setenv(config.EnvHome, home)

	tool := &ScreenshotTool{agentHome: agentHome, restrict: true}

	pinnedCtx := tools.WithAutoApproved(context.Background(), tools.AutoPin{
		Tool: tool.Name(),
		Paths: []tools.PinnedPath{
			{Real: filepath.Join(agentHome, "unrelated.jpg"), Access: fspolicy.PathGrantAccessRead},
		},
	})

	path, failure := tool.writeScreenshot(pinnedCtx, []byte("jpeg bytes"))
	if failure == nil {
		t.Fatalf("want the write-time re-check to refuse a write pinned only for read, got a write to %q", path)
	}
	if !failure.IsError {
		t.Errorf("want an error result, got %+v", failure)
	}
	entries, err := os.ReadDir(agentHome)
	if err != nil {
		t.Fatalf("read agent home: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("want nothing written when the re-check refuses, found %d entries in %q", len(entries), agentHome)
	}
}

// TestScreenshotTool_WriteScreenshot_RecheckAllowsMatchingPin is the
// positive control for the two tests above: an Auto pin that DOES cover
// write access for this tool must let writeScreenshot save the file, so
// the previous test's refusal is provably about the narrower access, not
// about RecheckAutoPin refusing every pinned call unconditionally.
func TestScreenshotTool_WriteScreenshot_RecheckAllowsMatchingPin(t *testing.T) {
	home := t.TempDir()
	agentHome := filepath.Join(home, "agents", "self")
	if err := os.MkdirAll(agentHome, 0o755); err != nil {
		t.Fatalf("mkdir agent home: %v", err)
	}
	t.Setenv(config.EnvHome, home)

	tool := &ScreenshotTool{agentHome: agentHome, restrict: true}

	pinnedCtx := tools.WithAutoApproved(context.Background(), tools.AutoPin{
		Tool: tool.Name(),
		Paths: []tools.PinnedPath{
			{Real: filepath.Join(agentHome, "unrelated.jpg"), Access: fspolicy.PathGrantAccessWrite},
		},
	})

	path, failure := tool.writeScreenshot(pinnedCtx, []byte("jpeg bytes"))
	if failure != nil {
		t.Fatalf("want the write to succeed when the pin covers write access, got %+v", failure)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file %q: %v", path, err)
	}
	if string(data) != "jpeg bytes" {
		t.Errorf("written content = %q, want %q", data, "jpeg bytes")
	}
}

// TestScreenshotTool_AutoApproveVerdict_ImplementsClassifier is a
// compile-time-ish guard that fails loudly if ScreenshotTool stops
// satisfying tools.AutoApproveClassifier.
func TestScreenshotTool_AutoApproveVerdict_ImplementsClassifier(t *testing.T) {
	var _ tools.AutoApproveClassifier = (*ScreenshotTool)(nil)
}
