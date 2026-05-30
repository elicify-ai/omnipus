// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — insider-LLM red-team coverage for cross-agent reads.
//
// Threats from the insider-pentest report:
//
//	C5-DIRECT  — agent A directly references agent B's path. The defense
//	             is `isCrossAgentPath` in pkg/tools/filesystem.go:97, which
//	             matches "another agent's workspace" by detecting a sibling
//	             entry under the agents/ directory and rejecting the path
//	             before the file open. Today: enforced by every tool that
//	             routes paths through validatePathWithAllowPaths. The test
//	             PASSES today and acts as a regression guard.
//
//	C5-SYMLINK — agent A creates a symlink in its OWN workspace pointing
//	             to agent B's secret file, then opens the symlink. The
//	             current `validatePathWithAllowPaths` resolves symlinks
//	             via filepath.EvalSymlinks BEFORE the within-workspace
//	             check, so the tool does see the real cross-agent target
//	             and rejects the read. Test PASSES today.
//
// Both tests are written as table-driven assertions over the FS-touching
// builtins. Adding a new FS tool that takes a path argument requires
// adding a row here, otherwise the new tool slips its way around the
// cross-agent guard unnoticed.

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/media"
)

// agentBSecretContent is the canonical sentinel string we plant in agent B's
// SOUL.md. If a tool in agent A's context returns this string, the
// cross-agent guard has been bypassed.
const agentBSecretContent = "REDTEAM_C5_SENTINEL_agent_B_secret_soul_content"

// crossAgentSetup builds two agent workspaces side-by-side under a shared
// agents/ root and returns: the agentA workspace path, the agentB workspace
// path, and a path inside agentB pointing at a sentinel file the test wants
// to deny access to. The mediaStore is built but kept on the test side —
// callers that need send_file pass it in.
type crossAgentSetup struct {
	homeDir       string // common parent (~/.omnipus)
	agentsDir     string // <home>/agents
	agentAWS      string // <agents>/agent-A
	agentBWS      string // <agents>/agent-B
	agentBSoul    string // <agentBWS>/SOUL.md (the sentinel)
	mediaStoreDir string // a tempdir for the media pipeline
}

func newCrossAgentSetup(t *testing.T) *crossAgentSetup {
	t.Helper()
	home := t.TempDir()
	agents := filepath.Join(home, "agents")
	wsA := filepath.Join(agents, "agent-A")
	wsB := filepath.Join(agents, "agent-B")
	for _, d := range []string{wsA, wsB} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	soul := filepath.Join(wsB, "SOUL.md")
	if err := os.WriteFile(soul, []byte(agentBSecretContent), 0o600); err != nil {
		t.Fatalf("write agent-B SOUL.md: %v", err)
	}
	mediaDir := filepath.Join(home, "media")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	return &crossAgentSetup{
		homeDir:       home,
		agentsDir:     agents,
		agentAWS:      wsA,
		agentBWS:      wsB,
		agentBSoul:    soul,
		mediaStoreDir: mediaDir,
	}
}

// crossAgentDirectCase parameterises a "agent A invokes <tool> with a path
// that points into agent B's workspace" test. Each case is an FS-touching
// builtin; we drive them through the same denial-or-error assertion.
type crossAgentDirectCase struct {
	toolName string
	// build constructs the tool, configured with agentA's workspace as
	// the restriction root. The tool must reject `target` because it
	// resolves into a sibling agent's workspace.
	build func(setup *crossAgentSetup) Tool
	// args returns the tool args naming the agent-B target.
	args func(setup *crossAgentSetup) map[string]any
	// resultIsDeny inspects the ToolResult and returns true if the tool
	// behaved correctly: error result OR a result that does NOT echo the
	// agent-B sentinel content.
	//
	// Some tools return SilentResult on success; for those we ONLY check
	// IsError. For read_file, we ALSO check that the ForLLM body does
	// not contain the sentinel string.
	resultIsDeny func(t *testing.T, name string, res *ToolResult) bool
}

// genericIsErrorDeny is a default predicate: the tool denied the call iff
// res.IsError is set. Used by tools that don't return content (write, edit,
// append, send_file).
func genericIsErrorDeny(t *testing.T, name string, res *ToolResult) bool {
	t.Helper()
	if res == nil {
		t.Errorf("%s: nil ToolResult", name)
		return false
	}
	if !res.IsError {
		t.Errorf("%s: expected error result for cross-agent path; got success: %s", name, res.ForLLM)
		return false
	}
	return true
}

// readDenyPredicate is stricter for read_file: the tool may either
// IsError, OR return success but NOT contain the sentinel content. The
// second case would be a defense-in-depth bug (silently empty result) but
// not a confidentiality breach. The breach is when sentinel content
// reaches the LLM.
func readDenyPredicate(t *testing.T, name string, res *ToolResult) bool {
	t.Helper()
	if res == nil {
		t.Errorf("%s: nil ToolResult", name)
		return false
	}
	if !res.IsError && strings.Contains(res.ForLLM, agentBSecretContent) {
		t.Errorf("%s: cross-agent BREACH — read_file returned agent-B SOUL content to agent-A context: %q",
			name, res.ForLLM)
		return false
	}
	return true
}

// TestRedteam_CrossAgent_DirectRead documents C5-DIRECT. For every
// FS-touching builtin we own, agent A's tool instance is constructed
// against agent A's workspace and the call references a path inside
// agent B. The expected outcome is denial (IsError=true) for every tool.
//
// Today: passes for every tool that routes through
// validatePathWithAllowPaths because that helper detects sibling-agent
// paths via isCrossAgentPath.
func TestRedteam_CrossAgent_DirectRead(t *testing.T) {
	t.Logf(
		"documents C5-DIRECT (cross-agent direct read) from insider-pentest report; current control is isCrossAgentPath",
	)

	cases := []crossAgentDirectCase{
		{
			toolName: "read_file",
			build: func(s *crossAgentSetup) Tool {
				return NewReadFileTool(s.agentAWS, true, MaxReadFileSize)
			},
			args: func(s *crossAgentSetup) map[string]any {
				return map[string]any{"path": s.agentBSoul}
			},
			resultIsDeny: readDenyPredicate,
		},
		{
			toolName: "write_file",
			build: func(s *crossAgentSetup) Tool {
				return NewWriteFileTool(s.agentAWS, true)
			},
			args: func(s *crossAgentSetup) map[string]any {
				return map[string]any{
					"path":      filepath.Join(s.agentBWS, "evil.txt"),
					"content":   "agent-A wrote into agent-B!",
					"overwrite": true,
				}
			},
			resultIsDeny: genericIsErrorDeny,
		},
		{
			toolName: "edit_file",
			build: func(s *crossAgentSetup) Tool {
				return NewEditFileTool(s.agentAWS, true)
			},
			args: func(s *crossAgentSetup) map[string]any {
				return map[string]any{
					"path":     s.agentBSoul,
					"old_text": agentBSecretContent,
					"new_text": "tampered by agent-A",
				}
			},
			resultIsDeny: genericIsErrorDeny,
		},
		{
			toolName: "append_file",
			build: func(s *crossAgentSetup) Tool {
				return NewAppendFileTool(s.agentAWS, true)
			},
			args: func(s *crossAgentSetup) map[string]any {
				return map[string]any{
					"path":    s.agentBSoul,
					"content": "agent-A appended here",
				}
			},
			resultIsDeny: genericIsErrorDeny,
		},
		{
			toolName: "send_file",
			build: func(s *crossAgentSetup) Tool {
				store := media.NewFileMediaStore()
				tool := NewSendFileTool(s.agentAWS, true, 0, store)
				tool.SetContext("cli", "test-chat")
				return tool
			},
			args: func(s *crossAgentSetup) map[string]any {
				return map[string]any{"path": s.agentBSoul}
			},
			resultIsDeny: genericIsErrorDeny,
		},
	}

	for _, c := range cases {
		t.Run(c.toolName, func(t *testing.T) {
			setup := newCrossAgentSetup(t)
			tool := c.build(setup)
			ctx := WithToolContext(context.Background(), "cli", "test-chat")
			ctx = WithAgentID(ctx, "agent-A")
			res := tool.Execute(ctx, c.args(setup))
			if !c.resultIsDeny(t, c.toolName, res) {
				return
			}
			t.Logf("C5-DIRECT %s: cross-agent path denied as expected (IsError=%v, body=%.120q)",
				c.toolName, res.IsError, res.ForLLM)
		})
	}
}

// TestRedteam_CrossAgent_SymlinkRead documents C5-SYMLINK. Agent A creates
// a symlink in its OWN workspace whose target is agent B's SOUL.md. Agent
// A then asks read_file / send_file to open the symlink by its in-workspace
// path. A naive sandbox check — "is the literal path argument under agent
// A's workspace?" — would say yes and let the read through. The defense
// requires resolving the symlink and re-checking the resolved target.
//
// Current implementation: validatePathWithAllowPaths calls
// filepath.EvalSymlinks AFTER the workspace-prefix check and rejects
// when the resolved path falls outside the workspace.
func TestRedteam_CrossAgent_SymlinkRead(t *testing.T) {
	t.Logf(
		"documents C5-SYMLINK (cross-agent symlink read) from insider-pentest report; current control is EvalSymlinks in validatePathWithAllowPaths",
	)

	cases := []crossAgentDirectCase{
		{
			toolName: "read_file_symlink",
			build: func(s *crossAgentSetup) Tool {
				return NewReadFileTool(s.agentAWS, true, MaxReadFileSize)
			},
			args: func(s *crossAgentSetup) map[string]any {
				return map[string]any{"path": filepath.Join(s.agentAWS, "shortcut")}
			},
			resultIsDeny: readDenyPredicate,
		},
		{
			toolName: "send_file_symlink",
			build: func(s *crossAgentSetup) Tool {
				store := media.NewFileMediaStore()
				tool := NewSendFileTool(s.agentAWS, true, 0, store)
				tool.SetContext("cli", "test-chat")
				return tool
			},
			args: func(s *crossAgentSetup) map[string]any {
				return map[string]any{"path": filepath.Join(s.agentAWS, "shortcut")}
			},
			resultIsDeny: genericIsErrorDeny,
		},
	}

	for _, c := range cases {
		t.Run(c.toolName, func(t *testing.T) {
			setup := newCrossAgentSetup(t)
			// Plant the symlink: <agentA>/shortcut -> <agentB>/SOUL.md
			shortcut := filepath.Join(setup.agentAWS, "shortcut")
			if err := os.Symlink(setup.agentBSoul, shortcut); err != nil {
				t.Fatalf("create symlink: %v", err)
			}
			tool := c.build(setup)
			ctx := WithToolContext(context.Background(), "cli", "test-chat")
			ctx = WithAgentID(ctx, "agent-A")
			res := tool.Execute(ctx, c.args(setup))
			if !c.resultIsDeny(t, c.toolName, res) {
				return
			}
			t.Logf("C5-SYMLINK %s: symlink-to-cross-agent denied as expected (IsError=%v, body=%.120q)",
				c.toolName, res.IsError, res.ForLLM)
		})
	}
}
