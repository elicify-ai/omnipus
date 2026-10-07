// delegate_cannot_report_test.go: #948 and #895 — refusals of a delegation launch are specific, visible, and honest about their cause.

package tools

import (
	"context"
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
