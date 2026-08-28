// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// message_parent_real_context_test.go is the regression test for #576:
// message_parent failed for every delegated subagent because
// pkg/tools/message_parent.go read the durable session record under
// tools.ToolTranscriptSessionID(ctx) (the PARENT's shared transcript session
// id, deliberately inherited by the child per pkg/agent/subturn.go's FR-6a
// cascade-cancel matching) instead of tools.ToolDelegateSessionID(ctx) (the
// child's OWN ADR-053 durable session_id, which is what
// pkg/tools/delegate.go actually persists the LifecycleRecord under).
//
// The existing pkg/tools/message_parent_test.go coverage used a
// withChildContext(sessionID) helper that sets ONLY
// tools.WithTranscriptSessionID(ctx, sessionID) and seeds the lifecycle
// record under that SAME id — which happens to make ToolTranscriptSessionID
// and ToolDelegateSessionID resolve to the same value in the test, exactly
// the conflation that hid this bug from every existing unit test. Production
// never has this property: subturn.go's spawnSubTurn stamps the child's
// OWN fresh delegate session_id via tools.WithDelegateSessionID(childCtx,
// childID) while separately inheriting the PARENT's transcript session id
// via tools.WithTranscriptSessionID(turnCtx, ts.opts.TranscriptSessionID) —
// two DIFFERENT values on every real delegated child.
//
// This test drives the REAL production chain end to end: a real
// *tools.DelegateTool spawning via the real AgentLoopSpawner (spawnSubTurn),
// and the delegated child calling the REAL *tools.MessageParentTool with the
// context spawnSubTurn actually constructs — not a hand-built shortcut. It
// fails before the one-line fix (message_parent.go:381) and passes after.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	mp576ParentTaskMarker  = "PARENT_TASK_MP576: delegate and have the child report progress"
	mp576ChildTaskMarker   = "CHILD_TASK_MP576: report progress to your parent, then finish"
	mp576ChildProgressText = "CHILD_PROGRESS_MP576: halfway there"
	mp576ChildFinalAnswer  = "CHILD_FINAL_MP576: done reporting"
)

// mp576RecordingProvider is a content-routed scripted provider (same
// technique as delegate_async_toolcall_completion_test.go's
// contentRoutedDelegateProvider) that additionally records every message it
// is ever handed, so the test can recover the delegate-minted session_id
// from the parent's own tool-result turn without any private/internal
// access into DelegateTool's state.
type mp576RecordingProvider struct {
	mu       sync.Mutex
	calls    int
	recorded []string
}

func (p *mp576RecordingProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	for _, m := range messages {
		if m.Content != "" {
			p.recorded = append(p.recorded, m.Content)
		}
	}
	p.mu.Unlock()

	var lastUser string
	hasToolResult := false
	for _, m := range messages {
		switch m.Role {
		case "user":
			lastUser = m.Content
		case "tool":
			hasToolResult = true
		}
	}

	switch {
	case strings.Contains(lastUser, mp576ChildTaskMarker) && !hasToolResult:
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{
					ID: "message_parent-0",
					Function: &providers.FunctionCall{
						Name:      "message_parent",
						Arguments: fmt.Sprintf(`{"kind":"progress","text":%q}`, mp576ChildProgressText),
					},
				},
			},
		}, nil
	case strings.Contains(lastUser, mp576ChildTaskMarker) && hasToolResult:
		return &providers.LLMResponse{Content: mp576ChildFinalAnswer}, nil
	case strings.Contains(lastUser, mp576ParentTaskMarker) && !hasToolResult:
		argsJSON := fmt.Sprintf(`{"task":%q,"async":true}`, mp576ChildTaskMarker)
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{ID: "delegate-0", Function: &providers.FunctionCall{Name: "delegate", Arguments: argsJSON}},
			},
		}, nil
	case strings.Contains(lastUser, mp576ParentTaskMarker) && hasToolResult:
		return &providers.LLMResponse{Content: "Delegated. I'll let you know."}, nil
	default:
		return nil, fmt.Errorf(
			"mp576RecordingProvider: no scripted route for lastUser=%q hasToolResult=%v", lastUser, hasToolResult,
		)
	}
}

func (p *mp576RecordingProvider) GetDefaultModel() string { return "mp576-mock" }

// extractDelegateSessionID recovers the delegate-minted session_id from
// whatever tool-result content the parent's turn saw — the SAME text
// executeAsync (pkg/tools/delegate.go) returns synchronously to the parent's
// delegate() tool call: "... (session_id: <uuid>) — running in background...".
func (p *mp576RecordingProvider) extractDelegateSessionID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	re := regexp.MustCompile(`\(session_id: ([^)]+)\)`)
	for _, content := range p.recorded {
		if m := re.FindStringSubmatch(content); m != nil {
			return m[1]
		}
	}
	return ""
}

// TestMessageParent_RealSpawnSubTurnContext_ChildCanMessageParent is the
// #576 regression test: it proves message_parent succeeds — and lands the
// message under the delegate-minted session_id — when driven through the
// REAL spawnSubTurn context construction, which stamps
// tools.ToolTranscriptSessionID and tools.ToolDelegateSessionID with
// DIFFERENT values (unlike message_parent_test.go's withChildContext
// shortcut).
func TestMessageParent_RealSpawnSubTurnContext_ChildCanMessageParent(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "mp576-mock"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}

	provider := &mp576RecordingProvider{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	storeDir := t.TempDir()
	lifecycleStore := session.NewLifecycleStore(filepath.Join(storeDir, "lifecycle"))
	inboxStore := session.NewMessageInboxStore(filepath.Join(storeDir, "inbox"))

	delegateTool := tools.NewDelegateTool(cfg.Agents.Defaults.DefaultModel.Model, cfg.Agents.Defaults.MaxTokens, 0)
	delegateTool.SetSpawner(NewSubTurnSpawner(al))
	delegateTool.SetLifecycleStore(lifecycleStore)
	delegateTool.SetDelegationDenyCheckerBackground(
		func(context.Context, string) *tools.DelegationDenial { return nil },
	)
	al.RegisterTool(delegateTool)

	messageParentTool := tools.NewMessageParentTool(inboxStore, lifecycleStore)
	messageParentTool.SetSessionMessagingEnabled(func() bool { return true })
	al.RegisterTool(messageParentTool)

	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				"delegate":       "allow",
				"message_parent": "allow",
			},
		})
	}

	const sessionKey = "test-session-mp576-real-context"
	ctx := context.Background()

	parentDone := make(chan struct{})
	var parentErr error
	go func() {
		defer close(parentDone)
		_, parentErr = al.ProcessDirectWithChannel(ctx, mp576ParentTaskMarker, sessionKey, "telegram", "88888")
	}()

	select {
	case <-parentDone:
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: parent turn never finished")
	}
	require.NoError(t, parentErr)

	// Wait for the delegate's async completion notification — proves the
	// child sub-turn ran to completion (including its message_parent call)
	// via the REAL spawnSubTurn path, not merely that the tool call was
	// attempted.
	select {
	case msg := <-msgBus.InboundChan():
		assert.Contains(t, msg.Content, mp576ChildFinalAnswer)
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: timed out waiting for the delegate's async completion notification")
	}

	delegateSessionID := provider.extractDelegateSessionID()
	require.NotEmpty(t, delegateSessionID, "could not recover the delegate-minted session_id from the parent's tool-result transcript")

	// The inbox is keyed by rec.ParentDurableKey (the PARENT's own
	// ToolTranscriptSessionID at `run` time) — NOT the sessionKey passed to
	// ProcessDirectWithChannel: for a non-webchat channel with no SessionID
	// yet, processMessage transparently creates a real channel session and
	// uses ITS id as the transcript session id (pkg/agent/loop.go's
	// resolveOrCreateChannelSession call in processMessage). Read the
	// authoritative value back off the durable LifecycleRecord itself rather
	// than assuming it equals sessionKey.
	rec, lerr := lifecycleStore.Load(delegateSessionID)
	require.NoError(t, lerr, "failed to load the delegate-minted LifecycleRecord")
	require.NotEmpty(t, rec.ParentDurableKey, "LifecycleRecord.ParentDurableKey must be set")

	// The core #576 assertion: the child's message_parent(progress) call
	// must have landed in the PARENT's inbox, keyed by the delegate-minted
	// session_id (tools.ToolDelegateSessionID) — not silently failed with
	// "no durable session record for this session" because message_parent.go
	// looked it up under tools.ToolTranscriptSessionID (the shared parent
	// transcript id) instead.
	msgs, _, _, err := inboxStore.Drain(rec.ParentDurableKey, delegateSessionID, "", 10)
	require.NoError(t, err, "Drain failed")
	if assert.Len(t, msgs, 1, "expected exactly 1 progress message in the parent's inbox") {
		kind, kerr := msgs[0].Discriminator()
		require.NoError(t, kerr)
		assert.Equal(t, "progress", kind)
		prog, perr := msgs[0].AsSessionMessageProgress()
		require.NoError(t, perr)
		assert.Equal(t, mp576ChildProgressText, prog.Text)
		assert.Equal(t, delegateSessionID, prog.SessionId,
			"the message's SessionId must be the child's OWN delegate session_id, "+
				"proving message_parent resolved ToolDelegateSessionID(ctx), not ToolTranscriptSessionID(ctx)")
	}
}
