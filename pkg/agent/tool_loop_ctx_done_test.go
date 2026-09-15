// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for runTurn's per-tool loop stopping on a done turn
// context (loop.go, the turnCtx.Err() check right after the hard-abort check
// at the top of `for i, tc := range normalizedToolCalls`).
//
// Before the check, only the hard-abort flag stopped a batch mid-way.
// ToolRegistry.ExecuteWithContext never consults the context, so a batch whose
// first call ran past the agent's own turn timeout (or whose turn context was
// cancelled by anything other than a hard abort) went on dispatching every
// queued call after it.
package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ctxDoneTriggerTool ends its own turn's context while it runs, then returns a
// normal successful result. mode "cancel" calls the turn's cancel function;
// mode "outlast" waits until the turn's deadline has passed.
type ctxDoneTriggerTool struct {
	tools.BaseTool
	name string
	mode string
}

func (c *ctxDoneTriggerTool) Name() string { return c.name }
func (c *ctxDoneTriggerTool) Description() string {
	return "test stub that ends its own turn context while running"
}

func (c *ctxDoneTriggerTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (c *ctxDoneTriggerTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (c *ctxDoneTriggerTool) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	switch c.mode {
	case "cancel":
		ts := turnStateFromContext(ctx)
		if ts == nil {
			return tools.ErrorResult("test setup: no turn state on the tool context")
		}
		ts.mu.RLock()
		cancel := ts.turnCancel
		ts.mu.RUnlock()
		if cancel == nil {
			return tools.ErrorResult("test setup: turn has no cancel function")
		}
		cancel()
	case "outlast":
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			return tools.ErrorResult("test setup: the turn deadline never passed")
		}
	}
	return tools.NewToolResult("done")
}

// newCtxDoneLoop builds an agent loop for agent "mia" with the given per-turn
// timeout (0 = none) and a default workspace on disk.
func newCtxDoneLoop(t *testing.T, provider providers.LLMProvider, timeoutSeconds int) *AgentLoop {
	t.Helper()
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	wsDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "ctxdone-default.json"),
		[]byte(`{"id":"ctxdone-default","is_default":true}`), 0o600))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				TimeoutSeconds:    timeoutSeconds,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	return al
}

// skippedToolReasons drains every event already published on sub and returns
// the ToolExecSkipped reasons recorded for toolName.
func skippedToolReasons(sub EventSubscription, toolName string) []string {
	var reasons []string
	for {
		select {
		case evt, ok := <-sub.C:
			if !ok {
				return reasons
			}
			if evt.Kind != EventKindToolExecSkipped {
				continue
			}
			if p, isSkip := evt.Payload.(ToolExecSkippedPayload); isSkip && p.Tool == toolName {
				reasons = append(reasons, p.Reason)
			}
		default:
			return reasons
		}
	}
}

func TestRunTurn_ToolBatch_TurnContextDone_StopsStartingQueuedCalls(t *testing.T) {
	cases := []struct {
		name           string
		mode           string
		timeoutSeconds int
		wantSentinel   error
		wantCause      error
	}{
		{
			name:         "turn_context_cancelled_mid_batch",
			mode:         "cancel",
			wantSentinel: ErrTurnCanceled,
			wantCause:    context.Canceled,
		},
		{
			name:           "agent_turn_timeout_passes_mid_batch",
			mode:           "outlast",
			timeoutSeconds: 2,
			wantSentinel:   ErrTurnTimedOut,
			wantCause:      context.DeadlineExceeded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := providers.FunctionCall{Name: "end_turn_ctx", Arguments: `{}`}
			second := providers.FunctionCall{Name: "queued_poll", Arguments: `{"status":"running"}`}
			third := providers.FunctionCall{Name: "queued_poll", Arguments: `{"status":"done"}`}
			provider := testutil.NewScenario().
				WithToolCalls([]providers.ToolCall{
					{ID: "call-1", Function: &first},
					{ID: "call-2", Function: &second},
					{ID: "call-3", Function: &third},
				}).
				WithText("unreachable: the turn must end before another provider round")

			al := newCtxDoneLoop(t, provider, tc.timeoutSeconds)
			trigger := &ctxDoneTriggerTool{name: "end_turn_ctx", mode: tc.mode}
			queued := &fixedResultTool{name: "queued_poll", result: `{"jobs":[]}`}
			al.RegisterTool(trigger)
			al.RegisterTool(queued)
			allowToolsForAllAgents(t, al, "end_turn_ctx", "queued_poll")

			sub := al.SubscribeEvents(256)
			defer al.UnsubscribeEvents(sub.ID)

			_, err := al.ProcessDirect(context.Background(), "run the batch", "test-session-ctx-done-"+tc.mode)

			require.Error(t, err, "a turn whose context is done must end with the typed exit error")
			assert.True(t, errors.Is(err, tc.wantSentinel), "error must wrap %v, got %v", tc.wantSentinel, err)
			assert.True(t, errors.Is(err, tc.wantCause), "error must wrap %v, got %v", tc.wantCause, err)
			assert.Equal(t, int32(0), queued.calls.Load(),
				"no tool call queued behind the one that ended the turn context may start")
			assert.Equal(t, 1, provider.CallCount(),
				"no further provider round may be spent once the turn context is done")

			reasons := skippedToolReasons(sub, "queued_poll")
			require.Len(t, reasons, 2, "both queued calls must be reported as skipped")
			for _, r := range reasons {
				assert.True(t, strings.HasPrefix(r, "turn context done ("),
					"skip reason must name the done turn context, got %q", r)
			}
		})
	}
}
