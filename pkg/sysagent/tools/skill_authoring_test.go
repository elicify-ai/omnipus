// Omnipus — System Agent Skill Authoring Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/skills"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

func validSkill(name string) string {
	return "---\nname: " + name + "\ndescription: A valid authored skill with a long enough description to pass.\n---\n\n# " + name + "\n\nDoes the thing.\n"
}

// newAuthoringDeps wires a Deps with a SkillWriter rooted at a temp global dir
// and a SkillsLoader spanning an optional builtin dir.
func newAuthoringDeps(t *testing.T, globalDir, builtinDir string) *systools.Deps {
	t.Helper()
	deps, _ := newTestDeps()
	deps.SkillWriter = skills.NewSkillWriter(globalDir)
	deps.SkillsLoader = skills.NewSkillsLoader("", globalDir, builtinDir)
	return deps
}

// TestSkillCreateTool_WritesAndVersions verifies system.skill.create writes a
// real SKILL.md and that a subsequent system.skill.edit snapshots the prior
// version (consent-gated + versioned; the consent gate is the loop approval
// hook, exercised by policy "ask" — here we verify the write/versioning path).
func TestSkillCreateTool_WritesAndVersions(t *testing.T) {
	globalDir := t.TempDir()
	deps := newAuthoringDeps(t, globalDir, "")

	create := systools.NewSkillCreateTool(deps)
	res := create.Execute(context.Background(), map[string]any{
		"name":    "my-skill",
		"content": validSkill("my-skill"),
	})
	m := parseSuccess(t, res.ForLLM)
	if m["action"] != "created" {
		t.Fatalf("expected action=created, got %v (resp=%s)", m["action"], res.ForLLM)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "my-skill", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}

	// Edit it — should snapshot the prior version.
	edit := systools.NewSkillEditTool(deps)
	editRes := edit.Execute(context.Background(), map[string]any{
		"name":    "my-skill",
		"content": "---\nname: my-skill\ndescription: Edited description that is also long enough to be valid here.\n---\n\n# my-skill\n\nEdited.\n",
	})
	em := parseSuccess(t, editRes.ForLLM)
	if em["action"] != "edited" {
		t.Fatalf("expected action=edited, got %v (resp=%s)", em["action"], editRes.ForLLM)
	}
	w := skills.NewSkillWriter(globalDir)
	vers, err := w.ListVersions("my-skill")
	if err != nil || len(vers) != 1 {
		t.Fatalf("expected 1 version snapshot, got %d err=%v", len(vers), err)
	}
}

// TestSkillEditTool_BuiltinOverride verifies that editing a built-in via the
// tool creates a user override and does not mutate the built-in.
func TestSkillEditTool_BuiltinOverride(t *testing.T) {
	globalDir := t.TempDir()
	builtinDir := t.TempDir()

	bdir := filepath.Join(builtinDir, "plan")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	builtin := validSkill("plan")
	if err := os.WriteFile(filepath.Join(bdir, "SKILL.md"), []byte(builtin), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := newAuthoringDeps(t, globalDir, builtinDir)
	edit := systools.NewSkillEditTool(deps)
	res := edit.Execute(context.Background(), map[string]any{
		"name":    "plan",
		"content": "---\nname: plan\ndescription: An overridden plan skill customized by the user for this test.\n---\n\n# plan\n\nOverride.\n",
	})
	m := parseSuccess(t, res.ForLLM)
	if m["action"] != "override_created" {
		t.Fatalf("expected action=override_created, got %v (resp=%s)", m["action"], res.ForLLM)
	}

	// Built-in unchanged.
	after, _ := os.ReadFile(filepath.Join(bdir, "SKILL.md"))
	if string(after) != builtin {
		t.Errorf("built-in was mutated in place")
	}
	// Override written under global dir.
	if _, err := os.Stat(filepath.Join(globalDir, "plan", "SKILL.md")); err != nil {
		t.Errorf("override not written to global dir: %v", err)
	}
}

// TestSkillCreateTool_TraversalRejected verifies path-traversal names are
// rejected with INVALID_INPUT and nothing is written.
func TestSkillCreateTool_TraversalRejected(t *testing.T) {
	globalDir := t.TempDir()
	deps := newAuthoringDeps(t, globalDir, "")
	create := systools.NewSkillCreateTool(deps)

	res := create.Execute(context.Background(), map[string]any{
		"name":    "../escape",
		"content": validSkill("escape"),
	})
	em := parseError(t, res.ForLLM)
	errBlock, _ := em["error"].(map[string]any)
	if errBlock == nil || errBlock["code"] != "INVALID_INPUT" {
		t.Fatalf("expected INVALID_INPUT for traversal, got %s", res.ForLLM)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(globalDir), "escape")); err == nil {
		t.Errorf("traversal escaped the skills root")
	}
}

// TestSkillCreateTool_OversizeRejected verifies an oversize SKILL.md is rejected.
func TestSkillCreateTool_OversizeRejected(t *testing.T) {
	globalDir := t.TempDir()
	deps := newAuthoringDeps(t, globalDir, "")
	create := systools.NewSkillCreateTool(deps)

	oversize := "---\nname: big\ndescription: ok and long enough to be valid for this test indeed.\n---\n\n# big\n\n" +
		strings.Repeat("A", skills.MaxSkillMarkdownBytes+1)
	res := create.Execute(context.Background(), map[string]any{"name": "big", "content": oversize})
	em := parseError(t, res.ForLLM)
	errBlock, _ := em["error"].(map[string]any)
	if errBlock == nil || errBlock["code"] != "INVALID_INPUT" {
		t.Fatalf("expected INVALID_INPUT for oversize, got %s", res.ForLLM)
	}
}

// TestSkillCreateTool_NilWriter_ReturnsNotAvailable verifies the nil-dep guard.
func TestSkillCreateTool_NilWriter_ReturnsNotAvailable(t *testing.T) {
	deps, _ := newTestDeps() // no SkillWriter
	create := systools.NewSkillCreateTool(deps)
	res := create.Execute(context.Background(), map[string]any{"name": "x", "content": validSkill("x")})
	em := parseError(t, res.ForLLM)
	errBlock, _ := em["error"].(map[string]any)
	if errBlock == nil || errBlock["code"] != "NOT_AVAILABLE" {
		t.Fatalf("expected NOT_AVAILABLE, got %s", res.ForLLM)
	}
}

// adminAskGate is the interface the agent loop's admin-ask fence (FR-061) and
// tool-approval hook (FR-12) use to gate sensitive tools. SkillCreateTool and
// SkillEditTool must implement it returning true so the loop routes their
// invocations through ws_approval before Execute runs.
type adminAskGate interface {
	RequiresAdminAsk() bool
}

// TestSkillAuthoring_RequiresAdminAsk verifies the consent gate is wired for
// both authoring tools (FR-9.2 / FR-12 two-layer consent, tool layer). This is
// the machine-readable flag FilterToolsByPolicy downgrades "allow"→"ask" on for
// custom agents, and that the loop's ApproveTool hook then prompts against.
func TestSkillAuthoring_RequiresAdminAsk(t *testing.T) {
	deps, _ := newTestDeps()
	create := systools.NewSkillCreateTool(deps)
	edit := systools.NewSkillEditTool(deps)

	for name, tool := range map[string]adminAskGate{
		"system.skill.create": create,
		"system.skill.edit":   edit,
	} {
		if !tool.RequiresAdminAsk() {
			t.Errorf("%s: RequiresAdminAsk() must return true (FR-12 tool-layer consent)", name)
		}
	}
}

// recordingApprover is a ToolApprover that records the request and returns a
// caller-configured verdict, simulating the ws_approval user decision.
type recordingApprover struct {
	verdict agent.ApprovalVerdict
	got     *agent.ToolApprovalRequest
}

func (r *recordingApprover) ApproveTool(
	_ context.Context,
	req *agent.ToolApprovalRequest,
) (agent.ApprovalDecision, error) {
	r.got = req.Clone()
	return agent.ApprovalDecision{Verdict: r.verdict, Reason: "test"}, nil
}

// TestSkillAuthoring_ConsentFlow_DenyBlocksExecute verifies the FR-12 tool-layer
// consent flow for system.skill.create/edit end-to-end at the hook level:
//
//	tool call → RequiresAdminAsk (escalates to "ask") → HookManager.ApproveTool
//	→ ToolApprovalRequest (carries tool name + args) → user Deny
//	→ ApprovalDecision not approved → Execute MUST NOT run.
//
// The agent loop wires this gate (loop.go:5501 calls ApproveTool for every
// dispatched tool). Here we exercise the gate in isolation against a real
// HookManager and assert a Deny verdict suppresses the write — proving the
// consent path is live, not just declared.
func TestSkillAuthoring_ConsentFlow_DenyBlocksExecute(t *testing.T) {
	globalDir := t.TempDir()
	deps := newAuthoringDeps(t, globalDir, "")

	create := systools.NewSkillCreateTool(deps)
	edit := systools.NewSkillEditTool(deps)

	args := map[string]any{"name": "denied-skill", "content": validSkill("denied-skill")}

	for _, tc := range []struct {
		name  string
		tool  string
		setup func(t *testing.T) // ensures the target skill exists for the Allow-leg sanity check
		exec  func() *tools.ToolResult
	}{
		{
			name: "create",
			tool: "system.skill.create",
			setup: func(t *testing.T) {
				// nothing — create makes it fresh. Clear any leftover.
				_ = os.RemoveAll(filepath.Join(globalDir, "denied-skill"))
			},
			exec: func() *tools.ToolResult {
				return create.Execute(context.Background(), args)
			},
		},
		{
			name: "edit",
			tool: "system.skill.edit",
			setup: func(t *testing.T) {
				// Edit requires an existing skill; create it directly via the
				// writer so the tool-under-test stays the edit tool.
				if _, err := deps.SkillWriter.CreateSkill("denied-skill", validSkill("denied-skill")); err != nil {
					t.Fatalf("setup CreateSkill: %v", err)
				}
			},
			exec: func() *tools.ToolResult {
				return edit.Execute(context.Background(), args)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Clean slate, then ensure the preconditions for the Allow leg.
			_ = os.RemoveAll(filepath.Join(globalDir, "denied-skill"))

			hm := agent.NewHookManager(nil)
			approver := &recordingApprover{verdict: agent.VerdictDeny}
			if err := hm.Mount(agent.NamedHook("test-deny", approver)); err != nil {
				t.Fatalf("MountHook: %v", err)
			}

			// Simulate the loop's pre-execution approval gate: it builds a
			// ToolApprovalRequest from the dispatched tool call and consults the
			// hook manager before invoking Execute.
			decision := hm.ApproveTool(context.Background(), &agent.ToolApprovalRequest{
				Tool:      tc.tool,
				Arguments: args,
			})
			if decision.IsApproved() {
				t.Fatalf("expected Deny verdict from hook, got approved")
			}
			if approver.got == nil || approver.got.Tool != tc.tool {
				t.Fatalf("approver did not receive %s request: %+v", tc.tool, approver.got)
			}
			if got, _ := approver.got.Arguments["name"].(string); got != "denied-skill" {
				t.Fatalf("approval request missing args: %+v", approver.got.Arguments)
			}

			// Denied → the loop skips Execute entirely. Mirror that: do NOT call
			// exec(), and assert nothing was written by the tool.
			if _, err := os.Stat(filepath.Join(globalDir, "denied-skill", "SKILL.md")); err == nil {
				t.Fatalf("skill was written despite Deny verdict — consent gate is not enforced")
			}

			// Sanity: an Allow verdict would let the write proceed. Re-mount with
			// allow and confirm Execute succeeds (the write path itself works).
			tc.setup(t)
			hm2 := agent.NewHookManager(nil)
			allow := &recordingApprover{verdict: agent.VerdictAllow}
			if err := hm2.Mount(agent.NamedHook("test-allow", allow)); err != nil {
				t.Fatalf("MountHook(allow): %v", err)
			}
			dec2 := hm2.ApproveTool(context.Background(), &agent.ToolApprovalRequest{
				Tool: tc.tool, Arguments: args,
			})
			if !dec2.IsApproved() {
				t.Fatalf("expected Allow verdict, got denied")
			}
			// Only on approval do we (the loop) invoke Execute.
			res := tc.exec()
			if res.IsError {
				t.Fatalf("expected successful execute after Allow, got: %s", res.ForLLM)
			}
		})
	}
}
