package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func captureLogs(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	buf := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedLogWriter{mu: &mu, buf: buf}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

type lockedLogWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *lockedLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func TestDelegate_RejectsRemovedArgs(t *testing.T) {
	for _, arg := range []string{"async", "allow_blocking_question"} {
		t.Run(arg, func(t *testing.T) {
			tool := NewDelegateTool("", 0, 0)
			result := tool.Execute(context.Background(), map[string]any{
				"action": "run",
				"task":   "inspect checkout",
				arg:      true,
			})
			if !result.IsError {
				t.Fatalf("removed argument %q was accepted: %s", arg, result.ForLLM)
			}
			want := "invalid_argument: " + arg
			if result.ForLLM != want {
				t.Fatalf("error = %q, want %q", result.ForLLM, want)
			}
		})
	}
}

type recordingSessionLauncher struct {
	launchReq          steer.LaunchRequest
	launchRes          steer.LaunchResult
	dispatch           steer.DispatchResult
	dispatchSessionID  string
	dispatchGeneration int
}

func u14PermissiveTool(t *testing.T) (*DelegateTool, *recordingSessionLauncher) {
	t.Helper()
	launcher := &recordingSessionLauncher{}
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSessionLauncher(launcher)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	return tool, launcher
}

func (f *recordingSessionLauncher) Launch(_ context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	f.launchReq = req
	if f.launchRes.SessionID == "" {
		f.launchRes = steer.LaunchResult{SessionID: "recording-launcher-child", Generation: 1}
	}
	return f.launchRes, nil
}

func (f *recordingSessionLauncher) Dispatch(_ context.Context, sessionID string, generation int) (steer.DispatchResult, error) {
	f.dispatchSessionID = sessionID
	f.dispatchGeneration = generation
	if f.launchRes.SessionID != "" && (sessionID != f.launchRes.SessionID || generation != f.launchRes.Generation) {
		return steer.DispatchResult{}, steer.ErrStaleGeneration
	}
	if f.dispatch.Generation == 0 {
		f.dispatch = steer.DispatchResult{State: steer.DispatchRunning, Generation: generation}
	}
	return f.dispatch, nil
}

func TestDelegateRun_UsesLauncherAndReportsQueuedDispatch(t *testing.T) {
	launcher := &recordingSessionLauncher{
		launchRes: steer.LaunchResult{SessionID: "child-1", Generation: 1},
		dispatch: steer.DispatchResult{
			State:            steer.DispatchQueued,
			ConcurrencyLimit: 2,
			QueuePosition:    3,
			Generation:       1,
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

	parts := strings.SplitN(result.ForLLM, "\n", 2)
	if len(parts) != 2 || !strings.Contains(parts[1], "concurrency limit 2") ||
		!strings.Contains(parts[1], "queue position 3") || !strings.Contains(parts[1], `delegate(action="cancel")`) {
		t.Fatalf("queued result lacks actionable cap notice: %q", result.ForLLM)
	}
	var response generated.DelegateSessionResponse
	if err := json.Unmarshal([]byte(parts[0]), &response); err != nil {
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
