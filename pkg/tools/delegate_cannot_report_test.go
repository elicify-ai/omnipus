// delegate_cannot_report_test.go: #948 and #895 — refusals of a delegation launch are specific, visible, and honest about their cause.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// failingLauncher is a SessionLauncher whose Launch always fails with err.
type failingLauncher struct {
	recordingSessionLauncher
	err error
}

func (f *failingLauncher) Launch(context.Context, steer.LaunchRequest) (steer.LaunchResult, error) {
	return steer.LaunchResult{}, f.err
}

func runWithLaunchErr(t *testing.T, launchErr error, extra map[string]any) *ToolResult {
	t.Helper()
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSessionLauncher(&failingLauncher{err: launchErr})
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	args := map[string]any{"task": "do it", "agent_id": "korn-ferry"}
	for k, v := range extra {
		args[k] = v
	}
	return tool.Execute(context.Background(), args)
}

func TestDelegateRun_TargetCannotReport_NamesAgentAndPolicy(t *testing.T) {
	t.Parallel()
	got := runWithLaunchErr(t, fmt.Errorf("steer: launch: %w: agent %q", ErrDelegateTargetCannotReport, "korn-ferry"), nil)
	if !got.IsError {
		t.Fatalf("expected an error result, got %+v", got)
	}
	for _, want := range []string{`"korn-ferry"`, "message_parent", "deny", "No child session was started", "not a failed or hung child"} {
		if !strings.Contains(got.ForLLM, want) {
			t.Errorf("refusal missing %q: %s", want, got.ForLLM)
		}
	}
	if strings.Contains(got.ForLLM, "delegate: launch:") {
		t.Errorf("must not fall through to the generic launch-error text: %s", got.ForLLM)
	}
}

func decodeSkillPayload(t *testing.T, res *ToolResult) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &m); err != nil {
		t.Fatalf("result is not the structured payload: %v\n%s", err, res.ForLLM)
	}
	return m
}

// #895: a requested_skill the target is not granted must not read as a
// delegation permission denial.
func TestDelegateRun_RequestedSkillNotGranted_IsNotAPermissionDenial(t *testing.T) {
	t.Parallel()
	got := runWithLaunchErr(t,
		fmt.Errorf("steer: launch: %w: skill %q requested for agent %q", ErrRequestedSkillDenied, "elicify-xlsx", "sofia"),
		map[string]any{"requested_skill": "elicify-xlsx", "agent_id": "sofia"})
	if !got.IsError {
		t.Fatalf("expected an error result, got %+v", got)
	}
	m := decodeSkillPayload(t, got)
	if m["error"] != SkillNotGrantedCode {
		t.Errorf("error code = %v, want %q", m["error"], SkillNotGrantedCode)
	}
	if m["error"] == DelegationDeniedCode || strings.Contains(got.ForLLM, "trust_set") {
		t.Errorf("must not be worded as a delegation denial: %s", got.ForLLM)
	}
	msg, _ := m["message"].(string)
	for _, want := range []string{`"sofia"`, `"elicify-xlsx"`, "delegation to", "is permitted", "not a delegation-permission problem"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
}

// ADR-072 FR-054: "no such skill" stays distinguishable from "not granted".
func TestDelegateRun_RequestedSkillUnknown_StaysDistinctFromNotGranted(t *testing.T) {
	t.Parallel()
	got := runWithLaunchErr(t,
		fmt.Errorf("steer: launch: %w: skill %q requested for agent %q", ErrRequestedSkillNotFound, "nope", "sofia"),
		map[string]any{"requested_skill": "nope", "agent_id": "sofia"})
	if m := decodeSkillPayload(t, got); m["error"] != SkillNotFoundCode {
		t.Errorf("error code = %v, want %q", m["error"], SkillNotFoundCode)
	}
}
