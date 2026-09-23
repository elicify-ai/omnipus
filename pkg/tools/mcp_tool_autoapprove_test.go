// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// boolPtr is a tiny helper — mcp.ToolAnnotations.DestructiveHint is *bool,
// and Go has no address-of-literal syntax.
func boolPtr(b bool) *bool { return &b }

// TestMCPTool_AutoApproveVerdict_T9 is ADR-092 D9's T9: the five MCP
// annotation shapes the design's §4 rule distinguishes, transcribed
// verbatim from docs/internal/specs/adr-092-auto-for-other-tools-design.md
// §4 and the T9 test row. Each case's want.Run is derived from the design
// document, never from AutoApproveVerdict's own source — a table entry
// flipped to match the implementation would defeat the test's purpose.
func TestMCPTool_AutoApproveVerdict_T9(t *testing.T) {
	cases := []struct {
		name        string
		annotations *mcp.ToolAnnotations
		wantRun     bool
		wantClass   string
	}{
		{
			name:        "readOnlyHint true runs",
			annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
			wantRun:     true,
			wantClass:   "mcp_not_destructive",
		},
		{
			name:        "destructiveHint false runs",
			annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false)},
			wantRun:     true,
			wantClass:   "mcp_not_destructive",
		},
		{
			name:        "destructiveHint true asks",
			annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(true)},
			wantRun:     false,
			wantClass:   "asks",
		},
		{
			name:        "no annotations at all asks",
			annotations: nil,
			wantRun:     false,
			wantClass:   "asks",
		},
		{
			name:        "annotations present, DestructiveHint nil, ReadOnlyHint false asks",
			annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: nil},
			wantRun:     false,
			wantClass:   "asks",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewMCPTool(&MockMCPManager{}, "test_server", &mcp.Tool{
				Name:        "some_tool",
				Annotations: tc.annotations,
			})
			verdict := tool.AutoApproveVerdict(context.Background(), map[string]any{"anything": "ignored"})
			if verdict.Run != tc.wantRun {
				t.Errorf("Run = %v, want %v (verdict=%+v)", verdict.Run, tc.wantRun, verdict)
			}
			if verdict.Class != tc.wantClass {
				t.Errorf("Class = %q, want %q", verdict.Class, tc.wantClass)
			}
			if verdict.Reason == "" {
				t.Error("want a non-empty Reason explaining the verdict")
			}
			if len(verdict.Paths) != 0 {
				t.Errorf("MCP tools have no J2 path condition; want no Paths, got %v", verdict.Paths)
			}
		})
	}
}

// TestMCPTool_AutoApproveVerdict_ImplementsClassifier is a compile-time-ish
// guard that fails loudly (rather than at a distant call site) if MCPTool
// stops satisfying AutoApproveClassifier.
func TestMCPTool_AutoApproveVerdict_ImplementsClassifier(t *testing.T) {
	var _ AutoApproveClassifier = (*MCPTool)(nil)
}
