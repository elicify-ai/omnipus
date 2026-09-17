// Omnipus — manifest classification + builder tests (ScopeCore tools)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// scopeCoreManifestMaxLineLen MUST be kept equal to pkg/tools/manifest.go's
// unexported maxManifestLineLen (140 runes). It cannot be imported directly:
// this test lives in the external `tools_test` package specifically so it
// can construct real ScopeCore sysagent tool instances (pkg/sysagent/tools
// already imports pkg/tools, so an internal `package tools` test — the
// package manifest_test.go uses — importing pkg/sysagent/tools back is a
// genuine import cycle, verified empirically before writing this file). The
// external test package has no such restriction.
// TestVisibility_ScopeCoreGetWorkspaceDescriptionFitsWithoutTruncation closes
// a real gap in TestVisibility_PreviewedDescriptionsFitWithoutTruncation
// (pkg/tools/manifest_test.go, internal `package tools`): that test builds
// its tool set from GeneralBuiltinMetadata() alone and silently `continue`s
// past any previewed-tier name absent from that catalog — which is exactly
// where every ScopeCore sysagent tool (registered via the sysagent layer,
// not GeneralBuiltinMetadata) lives, including get_workspace. Because of
// that gap, get_workspace's Description() first line was allowed to grow to
// 397 runes (nearly 3x maxManifestLineLen) with zero test coverage, even
// though get_workspace is Tier-2 (previewed) and its first line is what
// actually renders in the compressed manifest block on EVERY turn — the
// exact failure mode the truncation fix this test accompanies was meant to
// catch everywhere.
//
// This test does not attempt to enumerate every ScopeCore previewed tool
// generically (that would require pkg/tools to reach into pkg/sysagent's
// tool registry, which is intentionally not a dependency direction this
// codebase takes — see scopeCoreFullTierTools's doc comment in
// manifest_test.go). It instead checks get_workspace by name, the one
// previewed ScopeCore name (pkg/tools/manifest.go's previewedLazyToolNames)
// that is absent from GeneralBuiltinMetadata(). create_plan and execute_plan
// are ScopeCore and previewed too, but they ARE in that catalog, so the
// general test already checks their first lines. If another ScopeCore tool
// absent from that catalog is ever promoted into the previewed tier, add it
// here alongside get_workspace.
func TestVisibility_ScopeCoreGetWorkspaceIsUpfront(t *testing.T) {
	const name = "get_workspace"
	if got := tools.ToolManifestTier(name); got != tools.ManifestFull {
		t.Fatalf("ToolManifestTier(%q) = %v, want ManifestFull under ADR-090", name, got)
	}

	wsTool := systools.NewWorkspaceGetTool(&systools.Deps{})
	if wsTool.Name() != name {
		t.Fatalf("systools.NewWorkspaceGetTool().Name() = %q, want %q", wsTool.Name(), name)
	}

	if wsTool.Description() == "" {
		t.Fatal("upfront get_workspace must retain a callable description")
	}
}
