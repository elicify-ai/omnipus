package tools

import (
	"context"
	"encoding/json"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type recordingSessionLauncher struct {
	launchReq steer.LaunchRequest
	launchRes steer.LaunchResult
	dispatch  steer.DispatchResult
}

func (f *recordingSessionLauncher) Launch(_ context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	f.launchReq = req
	return f.launchRes, nil
}

func (f *recordingSessionLauncher) Dispatch(_ context.Context, sessionID string, generation int) (steer.DispatchResult, error) {
	if sessionID != f.launchRes.SessionID || generation != f.launchRes.Generation {
		return steer.DispatchResult{}, steer.ErrStaleGeneration
	}
	return f.dispatch, nil
}

func TestDelegateRun_UsesLauncherAndReportsQueuedDispatch(t *testing.T) {
	launcher := &recordingSessionLauncher{
		launchRes: steer.LaunchResult{SessionID: "child-1", Generation: 1},
		dispatch: steer.DispatchResult{
			State:         steer.DispatchQueued,
			QueuePosition: 3,
			Generation:    1,
		},
	}
	tool := NewDelegateTool("", 0, 0)
	tool.SetSessionLauncher(launcher)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "worker")
	ctx = WithToolCallID(ctx, "call-1")
	result := tool.Execute(ctx, map[string]any{
		"action":   "run",
		"agent_id": "worker",
		"label":    "Inspect checkout",
		"task":     "Inspect the checkout flow",
		"goal": map[string]any{
			"criteria": []any{map[string]any{
				"kind":     "prose",
				"judgment": "boolean",
				"text":     "The checkout defect is identified",
			}},
			"dod": []any{map[string]any{
				"kind":     "check",
				"judgment": "boolean",
				"text":     "Focused tests pass",
				"check": map[string]any{
					"command":            "go test ./pkg/checkout",
					"expected_exit_code": 0,
				},
			}},
		},
	})
	if result.IsError {
		t.Fatalf("delegate(run) returned error: %s", result.ForLLM)
	}

	var response generated.DelegateSessionResponse
	if err := json.Unmarshal([]byte(result.ForLLM), &response); err != nil {
		t.Fatalf("decode delegate response: %v\npayload: %s", err, result.ForLLM)
	}
	if response.SessionId != "child-1" || response.Generation != 1 {
		t.Fatalf("unexpected launched session: %+v", response)
	}
	if response.State != generated.DelegateSessionResponseStateQueued {
		t.Fatalf("state = %q, want queued", response.State)
	}
	if response.QueuePosition == nil || *response.QueuePosition != 3 {
		t.Fatalf("queue_position = %v, want 3", response.QueuePosition)
	}
	if launcher.launchReq.SteeringSessionID != "parent-1" {
		t.Fatalf("steering session = %q, want parent-1", launcher.launchReq.SteeringSessionID)
	}
	if launcher.launchReq.Origin.Kind != steer.OriginKindDelegate || launcher.launchReq.Origin.CallID != "call-1" {
		t.Fatalf("origin = %+v, want delegate/call-1", launcher.launchReq.Origin)
	}
	if launcher.launchReq.Goal == nil || len(launcher.launchReq.Goal.Criteria) != 1 || len(launcher.launchReq.Goal.DoD) != 1 {
		t.Fatalf("goal = %+v, want one criterion and one DoD item", launcher.launchReq.Goal)
	}
	if launcher.launchReq.Goal.DoD[0].Check == nil || launcher.launchReq.Goal.DoD[0].Check.Command != "go test ./pkg/checkout" {
		t.Fatalf("DoD check = %+v, want command preserved", launcher.launchReq.Goal.DoD[0].Check)
	}
	if result.Async {
		t.Fatal("launcher acknowledgement must be an ordinary immediate tool result, not a legacy async callback result")
	}
}
