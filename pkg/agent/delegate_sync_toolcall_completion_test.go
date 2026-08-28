// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W4 regression coverage, SYNC-PATH WIRING. buildSyncDelegateResult
// (delegate_result.go) has its own branch-complete unit test, but that pins
// only the pure function — not that loop.go actually CALLS it on the
// synchronous delegate branch. The original W4 bug WAS a call site quietly
// omitting a case (media handled, sync delegate not), so a helper-only test
// wouldn't catch a future refactor that deletes/misroutes the loop.go else-if
// (e.g. re-gates it behind cfg.Async) — it would compile clean and leave the
// helper test green while silently reintroducing the exact bug.
//
// This drives a REAL synchronous delegation through the production runTurn
// chain — a real *tools.DelegateTool + spawner on a real AgentLoop — and reads
// the PERSISTED transcript back (as a session reload does), asserting the
// delegate tool_call record carries the sub-turn's result. Sync needs none of
// the async test's gate/race machinery: async=false blocks the parent inside
// the delegate tool call until the child finishes, so ordering is deterministic
// by construction. Counterpart to delegate_async_toolcall_completion_test.go.

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	syncDelegateParentTask  = "SYNC_PARENT_TASK-7b2f: delegate this and report back"
	syncDelegateChildMarker = "SYNC_CHILD_MARKER-7b2f: do the thing and report"
	syncDelegateChildResult = "SYNC_DELEGATE_RESULT-7b2f-the-delegate-produced-this"
	syncDelegateParentFinal = "All done — the delegate finished."
)

// syncDelegateProvider scripts a parent turn that delegates SYNCHRONOUSLY
// (async=false) to a child that answers directly (no tool call), routed by the
// last user message — the same content-routing approach as
// contentRoutedDelegateProvider but without the async race, so no gate tool.
type syncDelegateProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *syncDelegateProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
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
	case strings.Contains(lastUser, syncDelegateChildMarker):
		// Child sub-turn: a direct final answer, no tool call — the simplest
		// delegation. This text becomes the delegate's result.ForLLM.
		return &providers.LLMResponse{Content: syncDelegateChildResult}, nil
	case strings.Contains(lastUser, syncDelegateParentTask) && !hasToolResult:
		// Parent iteration 1: delegate SYNCHRONOUSLY (async=false, the default).
		args := fmt.Sprintf(`{"task":%q,"async":false}`, syncDelegateChildMarker)
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{ID: "delegate-sync-0", Function: &providers.FunctionCall{Name: "delegate", Arguments: args}},
			},
		}, nil
	case strings.Contains(lastUser, syncDelegateParentTask) && hasToolResult:
		// Parent iteration 2: final answer, after the blocking delegate returned.
		return &providers.LLMResponse{Content: syncDelegateParentFinal}, nil
	default:
		return nil, fmt.Errorf(
			"syncDelegateProvider: no scripted route for lastUser=%q hasToolResult=%v",
			lastUser, hasToolResult,
		)
	}
}

func (p *syncDelegateProvider) GetDefaultModel() string { return "content-routed-mock" }

// TestRunTurn_SyncDelegate_PersistsResultOnReload is the call-site wiring guard:
// a real synchronous delegation must leave its result on the persisted delegate
// tool_call record (loop.go must invoke buildSyncDelegateResult on the sync
// branch), so a session reload shows what the delegate produced.
func TestRunTurn_SyncDelegate_PersistsResultOnReload(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "content-routed-mock"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}

	provider := &syncDelegateProvider{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)

	delegateTool := tools.NewDelegateTool(cfg.Agents.Defaults.DefaultModel.Model, cfg.Agents.Defaults.MaxTokens, 0)
	delegateTool.SetSpawner(NewSubTurnSpawner(al))
	// ADR-057 U14 fixture repair: DelegateTool now refuses to start a
	// delegated session without a durable lifecycle store wired (fail-closed
	// rather than an untracked, unrecoverable session) — mirrors
	// message_parent_real_context_test.go's wiring.
	lifecycleStore := session.NewLifecycleStore(filepath.Join(t.TempDir(), "lifecycle"))
	delegateTool.SetLifecycleStore(lifecycleStore)
	// Install BOTH gates (allow): async delegation uses the Background gate, but
	// this test delegates synchronously (async=false), which is gated by the
	// Await checker — without it the delegation is denied by default.
	delegateTool.SetDelegationDenyCheckerBackground(
		func(context.Context, string) *tools.DelegationDenial { return nil },
	)
	delegateTool.SetDelegationDenyCheckerAwait(
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

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "telegram", "main")
	require.NoError(t, err)
	sessionID := meta.ID

	// Drive processMessage directly with SessionID pre-set so the turn records
	// its transcript under OUR known session (loop.go:5100 skips
	// resolveOrCreateChannelSession when SessionID is already populated, and
	// loop.go:5122 then uses it as transcriptSessionID). ProcessDirectWithChannel
	// leaves SessionID empty, which would mint a fresh channel session we
	// couldn't address for the read-back.
	msg := bus.InboundMessage{
		Channel:    "telegram",
		Sender:     bus.SenderInfo{CanonicalID: "cron"},
		ChatID:     "77777",
		Content:    syncDelegateParentTask,
		SessionID:  sessionID,
		SessionKey: sessionID,
	}
	_, _, err = al.processMessage(context.Background(), msg)
	require.NoError(t, err)

	// Read the PERSISTED transcript back, exactly as a session reload does.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)

	var found bool
	for _, e := range entries {
		for _, tc := range e.ToolCalls {
			if tc.Tool != "delegate" {
				continue
			}
			found = true
			require.Equalf(t, "success", tc.Status,
				"sync delegate should have completed successfully; got %+v", tc)
			require.NotNilf(t, tc.Result,
				"W4 sync path: the persisted delegate tool_call Result must be populated, not nil "+
					"(loop.go must call buildSyncDelegateResult on the sync branch)")
			text, ok := tc.Result["text"].(string)
			require.Truef(t, ok && text != "",
				"persisted delegate Result[\"text\"] must carry the sub-turn's output; got %+v", tc.Result)
			require.Containsf(t, text, syncDelegateChildResult,
				"the persisted result must contain the child's ACTUAL output, not a placeholder")
		}
	}
	require.True(t, found, "expected a persisted delegate tool_call entry in the transcript")
}
