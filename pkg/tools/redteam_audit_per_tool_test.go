// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — insider-LLM red-team coverage for per-tool audit emission.
//
// Threat C7 (audit-blind tool) from the insider-pentest report: a tool that
// fails to route through the registry's audit emission path is a blind spot.
// An LLM that successfully calls such a tool would leave NO audit trail.
//
// SEC-15 mandates that EVERY tool execution produces an audit entry —
// allow OR deny. The mechanism is `ToolRegistry.ExecuteWithContext` (in
// pkg/tools/registry.go:225-370): every successful or errored execution
// dispatches a `tool_call` audit entry before returning. This is the
// chokepoint test — as long as a tool is invoked through the registry,
// it inherits audit coverage.
//
// This test walks a representative set of FS-touching, compute-only, and
// content-emitting builtins, registers each on a fresh registry wired to
// an in-process audit logger, invokes each through `ExecuteWithContext`,
// and asserts that the audit.jsonl contains AT LEAST ONE entry per tool.
//
// Today: passes — the registry chokepoint is in place.
//
// If a future PR introduces a tool execution path that bypasses
// ExecuteWithContext (e.g. a tool that's invoked directly, or a parallel
// dispatcher), the corresponding test row will not be wired up and the
// gap is documented as a missed coverage row in the registry catalog.

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// auditableToolFixture is the per-tool table row. Each entry knows how to
// build a tool against a fresh test workspace and how to invoke it with
// arguments that produce a deterministic outcome.
type auditableToolFixture struct {
	// toolName matches the tool's Tool.Name(). Used for audit assertion.
	toolName string
	// build constructs a fresh tool for one test row. workspace is a
	// fresh tempdir per-row so concurrent rows don't collide.
	build func(workspace string) Tool
	// args is the invocation payload. Should be valid against the tool's
	// schema and produce a non-panicking result (success or error are
	// both fine — both should emit an audit entry).
	args map[string]any
}

// TestRedteam_PerToolAuditCoverage walks a curated set of builtins and
// verifies each one produces at least one audit entry per Execute call.
// The set covers the FS-touching, content-only, and listing tool surfaces
// — the same surface an attacker would target for unobserved actions.
//
// Documents threat C7 from the insider-pentest report.
//
// Note: We do NOT walk every builtin in the static catalog. Several
// require external dependencies (skills.RegistryManager, taskstore.TaskStore,
// SubagentManager, MCPManager) whose construction is out of scope here.
// The chokepoint guarantee is at the registry level — if even one tool
// from each construction style is covered, the chokepoint property holds.
// New tools added to the registry inherit coverage automatically.
func TestRedteam_PerToolAuditCoverage(t *testing.T) {
	t.Logf(
		"documents C7 (audit blind tool) from insider-pentest report; current control is registry chokepoint at ExecuteWithContext",
	)

	// Build a curated table of builtins whose constructors don't require
	// external services. These are the FS-touching tools that an attacker
	// would most likely target for stealthy actions.
	fixtures := []auditableToolFixture{
		{
			toolName: "read_file",
			build: func(workspace string) Tool {
				// Seed a small file inside the workspace so the read succeeds.
				path := filepath.Join(workspace, "fixture.txt")
				if err := os.WriteFile(path, []byte("audit-fixture-content"), 0o600); err != nil {
					panic(err)
				}
				return NewReadFileTool(workspace, true, MaxReadFileSize)
			},
			args: map[string]any{"path": "fixture.txt"},
		},
		{
			toolName: "write_file",
			build: func(workspace string) Tool {
				return NewWriteFileTool(workspace, true)
			},
			args: map[string]any{
				"path":      "out.txt",
				"content":   "audit-fixture-write",
				"overwrite": true,
			},
		},
		{
			toolName: "edit_file",
			build: func(workspace string) Tool {
				path := filepath.Join(workspace, "edit.txt")
				if err := os.WriteFile(path, []byte("hello world"), 0o600); err != nil {
					panic(err)
				}
				return NewEditFileTool(workspace, true)
			},
			args: map[string]any{
				"path":     "edit.txt",
				"old_text": "world",
				"new_text": "audit",
			},
		},
		{
			toolName: "append_file",
			build: func(workspace string) Tool {
				return NewAppendFileTool(workspace, true)
			},
			args: map[string]any{
				"path":    "log.txt",
				"content": "appended-content",
			},
		},
		{
			toolName: "list_dir",
			build: func(workspace string) Tool {
				return NewListDirTool(workspace, true)
			},
			args: map[string]any{"path": "."},
		},
		{
			toolName: "message",
			build: func(_ string) Tool {
				return NewMessageTool()
			},
			// MessageTool's required field is `content`, not `message`.
			// Schema validation runs BEFORE the audit emission in the
			// registry, so an args mismatch would suppress the audit
			// entry — which would mask the gap rather than document it.
			args: map[string]any{"content": "audit-fixture-message"},
		},
	}

	for _, f := range fixtures {
		t.Run(f.toolName, func(t *testing.T) {
			workspace := t.TempDir()
			auditDir := t.TempDir()

			// Build the tool BEFORE constructing the registry so any seed
			// files (read_file's fixture, edit_file's source, etc.) are in
			// place by the time Execute runs.
			tool := f.build(workspace)

			logger, err := audit.NewLogger(audit.LoggerConfig{
				Dir:           auditDir,
				RetentionDays: 90,
			})
			require.NoError(t, err)
			defer logger.Close()

			reg := NewToolRegistry()
			reg.SetAuditLogger(logger)
			reg.Register(tool)

			ctx := WithAgentID(context.Background(), "redteam-agent")
			ctx = WithToolContext(ctx, "cli", "test-chat")

			// Sanity: tool name registered correctly.
			require.Equal(t, f.toolName, tool.Name(),
				"fixture toolName must match tool.Name() — registry would never find it otherwise")

			res := reg.ExecuteWithContext(ctx, f.toolName, f.args, "cli", "test-chat", nil)
			require.NotNil(t, res, "tool result is nil — registry contract violation")

			// Force flush so we can read the JSONL.
			require.NoError(t, logger.Close())

			// Locate audit.jsonl, read entries, and find one matching our tool.
			data, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
			require.NoError(t, err, "audit.jsonl not written for tool %q", f.toolName)

			lines := splitNonEmpty(strings.Split(string(data), "\n"))
			require.NotEmpty(t, lines, "audit.jsonl is empty for tool %q", f.toolName)

			matched := false
			for _, line := range lines {
				var entry map[string]any
				if jerr := json.Unmarshal([]byte(line), &entry); jerr != nil {
					t.Logf("audit line not JSON (skipping): %s", line)
					continue
				}
				if event, _ := entry["event"].(string); event != audit.EventToolCall {
					continue
				}
				if toolName, _ := entry["tool"].(string); toolName != f.toolName {
					continue
				}
				// Must have decision and agent_id populated per SEC-15.
				if dec, _ := entry["decision"].(string); dec == "" {
					t.Errorf("C7 PARTIAL: %s audit entry missing decision field: %s",
						f.toolName, line)
					continue
				}
				if aid, _ := entry["agent_id"].(string); aid != "redteam-agent" {
					t.Errorf("C7 PARTIAL: %s audit entry agent_id mismatch (got %q, want %q): %s",
						f.toolName, aid, "redteam-agent", line)
					continue
				}
				matched = true
				break
			}
			if !matched {
				t.Errorf("C7 GAP: %s did NOT produce a matching audit entry. Audit content was:\n%s",
					f.toolName, string(data))
			} else {
				t.Logf("C7 %s: audit entry produced as expected", f.toolName)
			}
		})
	}
}

// splitNonEmpty trims and drops empty strings from a slice (newline split
// of JSONL leaves a trailing empty when the file ends with \n).
func splitNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
