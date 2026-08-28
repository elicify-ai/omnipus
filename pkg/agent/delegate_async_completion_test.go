// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Closes the FR-N11 cross-spec traceability gap (gap 4 of the ADR-036
// fix-wave test-coverage review): `delegate`'s async completion path is
// provably correct by reading the code — pkg/agent/loop.go's generic
// asyncCallback closure sets AsyncNotifyEvent.SourceKind to whatever toolName
// was actually dispatched (asyncToolName := toolName), and `delegate` is
// registered under that exact name (DelegateTool.Name() == "delegate") — but
// had no direct test proving it. This file adds one.
//
// Traces to: docs/internal/specs/async-notifier-spec.md FR-N11 (line 350),
// Test Implementation Order #11 (line 305), BDD Scenario "delegate's async
// completion calls Notify with SourceKind \"delegate\"" (line 251).

package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestDelegateAsyncCompletion_CallsNotifyWithSourceKindDelegate drives the
// same real production chain as TestBashRunInBackground_CompletionTriggersNewTurn
// (registry.ExecuteWithContext -> DelegateTool.ExecuteAsync -> a REAL spawned
// sub-turn -> loop.go's generic asyncCallback -> AsyncNotifier.Notify ->
// bus.PublishInbound), but for `delegate` instead of `bash` — mirroring FR-N9's
// bash coverage exactly for this second producer, per FR-N11.
//
// The scripted provider gives every step AFTER the initial delegate tool-call
// a plain text (no further tool call) response: which of the two logically
// concurrent continuations (the parent turn's own next turn, vs. the spawned
// child sub-turn) consumes which specific scripted string is a real race
// (the child sub-turn is launched in a goroutine the instant the parent's
// tool call returns), but since neither string triggers a tool call, the
// race does not affect this test's assertions — both threads simply finish
// on their next Chat() call, and the child's completion feeds into
// DelegateTool's cb -> AsyncNotifier.Notify regardless of which text it drew.
func TestDelegateAsyncCompletion_CallsNotifyWithSourceKindDelegate(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall("delegate", `{"task":"summarize the attached notes","async":true}`).
		WithText("Delegated the task; I'll follow up once it's done.").
		WithText("The delegated sub-turn's own reply.")

	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	// Register a real DelegateTool wired to a real spawner — mirrors
	// loop.go's own production wiring (NewDelegateTool + SetSpawner +
	// NewSubTurnSpawner) at registration time, line for line. This test's
	// concern is the async-completion notification chain, not the
	// delegation-policy gate itself — wire a permissive background
	// deny-checker so the delegate call actually reaches the spawner (the
	// gate is fail-CLOSED when unwired per the 7-reviewer-gate follow-up to
	// ADR-037 — see TestDelegateTool_BackgroundNilDenyChecker_FailsClosed in
	// pkg/tools).
	delegateTool := tools.NewDelegateTool(cfg.Agents.Defaults.DefaultModel.Model, cfg.Agents.Defaults.MaxTokens, 0)
	delegateTool.SetSpawner(NewSubTurnSpawner(al))
	// ADR-057 U14 fixture repair: DelegateTool now refuses to start a
	// delegated session without a durable lifecycle store wired (fail-closed
	// rather than an untracked, unrecoverable session) — mirrors
	// message_parent_real_context_test.go's wiring.
	lifecycleStore := session.NewLifecycleStore(filepath.Join(t.TempDir(), "lifecycle"))
	delegateTool.SetLifecycleStore(lifecycleStore)
	delegateTool.SetDelegationDenyCheckerBackground(
		func(context.Context, string) *tools.DelegationDenial { return nil },
	)
	al.RegisterTool(delegateTool)

	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"delegate": "allow"},
		})
	}

	const sessionKey = "test-session-delegate-async"
	ctx := context.Background()
	// NOTE: which scripted text string ends up as the PARENT turn's own final
	// reply vs. the CHILD sub-turn's reply is a genuine race (see the doc
	// comment above) — do not assert on firstTurnResp's specific content here.
	_, err := al.ProcessDirectWithChannel(ctx, "please delegate this task", sessionKey, "telegram", "55555")
	require.NoError(t, err)

	var msg bus.InboundMessage
	select {
	case msg = <-msgBus.InboundChan():
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: timed out waiting for delegate's async completion notification — " +
			"FR-N11 wiring (delegate -> AsyncNotifier with SourceKind \"delegate\") appears broken or dropped")
	}

	assert.Equal(t, "system", msg.Channel)
	assert.Equal(t, "async:delegate", msg.Sender.CanonicalID,
		"FR-N11: delegate's async completion must call Notify with SourceKind \"delegate\"")
	assert.Equal(t, "telegram:55555", msg.ChatID, "must carry the ORIGINATING channel/chat")
}
