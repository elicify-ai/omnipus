// Omnipus — manifest classification + builder tests (ScopeCore tools)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools_test

import (
	"strings"
	"testing"
	"unicode/utf8"

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
const scopeCoreManifestMaxLineLen = 140

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
// ScopeCore name currently in previewedLazyToolNames
// (pkg/tools/manifest.go). If a second ScopeCore tool is ever promoted into
// the previewed tier, add it here alongside get_workspace.
func TestVisibility_ScopeCoreGetWorkspaceDescriptionFitsWithoutTruncation(t *testing.T) {
	const name = "get_workspace"

	// Sanity-check the premise: get_workspace really is in the previewed
	// (Tier 2) set this test exists to guard. If it is ever demoted out of
	// previewedLazyToolNames, this test's whole reason for existing goes
	// away — fail loudly rather than silently passing on an irrelevant tool.
	found := false
	for _, n := range tools.PreviewedLazyToolNames() {
		if n == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("%q is no longer in tools.PreviewedLazyToolNames() — this test is stale and should be updated or removed", name)
	}

	wsTool := systools.NewWorkspaceGetTool(&systools.Deps{})
	if wsTool.Name() != name {
		t.Fatalf("systools.NewWorkspaceGetTool().Name() = %q, want %q", wsTool.Name(), name)
	}

	raw := wsTool.Description()
	line := raw
	if idx := strings.IndexByte(raw, '\n'); idx >= 0 {
		line = raw[:idx]
	}
	line = strings.TrimSpace(line)
	if n := utf8.RuneCountInString(line); n > scopeCoreManifestMaxLineLen {
		t.Errorf(
			"previewed ScopeCore tool %q Description() first line is %d runes (> %d) and will be silently truncated in the manifest preview; give it a short, self-contained opening line: %q",
			name, n, scopeCoreManifestMaxLineLen, line,
		)
	}
}
