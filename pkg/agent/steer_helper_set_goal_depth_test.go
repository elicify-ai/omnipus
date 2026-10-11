// Omnipus — work-first goal spec FR-005: set_goal refuses at delegation depth
// > 0. A steered delegate helper launched WITH a goal owns a session-bound goal
// record; its turn must carry a delegation depth so the existing guard fires
// and the helper cannot amend that record.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestSteeredHelperWithGoal_SetGoalRefusedAndRecordUntouched(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	t.Setenv("OMNIPUS_HOME", home)
	provider := testutil.NewScenario().
		WithToolCall("set_goal", `{"mode":"update","definition":"AMENDED BY HELPER","criteria":[{"text":"helper rewrote this","judgment":"boolean"}],`+
			`"dod":[{"text":"helper rewrote dod","judgment":"boolean","provenance":"stated"}],"assessment":{"clarity":"clear"}}`).
		WithText("done")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens: 4096, MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testDefaultAgentID, Home: home}},
		},
	}
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	mintGenuineBootEpochForLoop(t, al)
	t.Cleanup(func() { al.Close() })
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)

	launcher := NewSteerLauncher(al)
	parent := newTestSteeringSession(t, al, "")
	launch, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent,
		TargetAgentID:     testDefaultAgentID,
		Task:              "do the bounded work",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-helper-goal"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the original criterion"}},
			DoD:      []steer.Criterion{{Text: "the original definition of done"}},
		},
	})
	require.NoError(t, err, "Launch(helper with goal)")
	rec, err := lifecycle.Load(launch.SessionID)
	require.NoError(t, err)
	require.NotEmpty(t, rec.GoalRef, "the helper must own a launch goal")
	before, err := resolveGoalRecordStore().Get(rec.GoalRef)
	require.NoError(t, err)

	_, err = launcher.Dispatch(context.Background(), launch.SessionID, launch.Generation)
	require.NoError(t, err, "Dispatch(helper)")
	waitFor(t, 15*time.Second, func() bool { return provider.CallCount() >= 2 })
	if ts := al.getActiveTurnState(launch.SessionID); ts != nil {
		select {
		case <-ts.Finished():
		case <-time.After(15 * time.Second):
			t.Fatal("helper turn did not finish")
		}
	}

	var toolResult string
	for _, req := range provider.AllRequests() {
		for _, m := range req {
			if m.Role == "tool" {
				toolResult = m.Content
			}
		}
	}
	require.Contains(t, toolResult, "owner-session-only",
		"set_goal from a steered delegate helper must be refused by the delegation-depth guard (FR-005)")

	after, err := resolveGoalRecordStore().Get(rec.GoalRef)
	require.NoError(t, err)
	require.Equal(t, before, after, "a refused set_goal must leave the helper's goal record untouched")
}
