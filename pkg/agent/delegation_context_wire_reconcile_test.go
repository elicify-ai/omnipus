// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestBuildDelegationContext_UsesSessionIDNotDeprecatedTaskID covers finding
// 4 (context-audit 2026-08): the rendered "## Delegation" block used to teach
// `delegate(action="status", task_id="…")` — task_id is a DEPRECATED alias in
// the live delegate tool (pkg/tools/delegate.go), and session_id is the
// current, non-deprecated parameter. This proves the poll instruction now
// uses session_id.
func TestBuildDelegationContext_UsesSessionIDNotDeprecatedTaskID(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", nil, ptr(3)),
	}
	got := buildDelegationContext(targets, 3)

	if !strings.Contains(got, `delegate(action="status", session_id="…")`) {
		t.Errorf("expected the poll instruction to use session_id; got:\n%s", got)
	}
	if strings.Contains(got, "task_id") {
		t.Errorf("rendered block still references the deprecated task_id alias; got:\n%s", got)
	}
}

// TestBuildDelegationContext_CreateTaskAdvertisesCriteria covers finding 4's
// second half: create_task requires a non-empty `criteria` array (rejected
// without one — pkg/tools/task.go's TaskCreateTool.Parameters "required"
// list), but the rendered signature used to omit it entirely, teaching a call
// shape that always gets rejected. This proves criteria now appears, and that
// the block flags create_task as needing ToolSearch to load first (it is a
// previewed, not full-manifest, tool — pkg/tools/manifest.go).
func TestBuildDelegationContext_CreateTaskAdvertisesCriteria(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", []config.DelegationMode{config.DelegationModeTask}, nil),
	}
	got := buildDelegationContext(targets, 3)

	if !strings.Contains(got, "criteria") {
		t.Errorf("rendered create_task call must advertise the required criteria param; got:\n%s", got)
	}
	if !strings.Contains(got, "ToolSearch") {
		t.Errorf("rendered create_task call must note it may need ToolSearch to load first; got:\n%s", got)
	}
}

// TestBuildDelegationContext_DocCommentDoesNotHandCopyDelegateSchema is a
// lightweight guard against finding 4's root cause recurring: the function's
// doc comment must point at DelegateTool.Description()/Parameters() as the
// source of truth for the tool's full schema, rather than embedding a second,
// driftable copy of it (the two-value action enum / task_id-only version that
// caused this finding in the first place).
func TestBuildDelegationContext_DocCommentDoesNotHandCopyDelegateSchema(t *testing.T) {
	data, err := os.ReadFile("delegation_context.go")
	if err != nil {
		t.Fatalf("read delegation_context.go: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "DelegateTool.Description()") && !strings.Contains(src, "DelegateTool.Parameters()") {
		t.Error("buildDelegationContext's doc comment no longer points at DelegateTool.Description()/Parameters() as the single source of truth")
	}
	// The retired two-value enum and task_id-required phrasing must not
	// reappear as a hand-copied schema fragment in the doc comment.
	if strings.Contains(src, "action (enum run|status") {
		t.Error("doc comment still hand-copies the retired 2-value action enum — finding 4 regression")
	}
}
