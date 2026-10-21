package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// ceilingDrainTool holds the first real dispatch at the process edge, so the
// follow-up cannot accidentally arrive before the turn or after its final poll.
// All scheduling, queue consumption and persistence remain production code.
type ceilingDrainTool struct {
	truncationEchoTool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (tool *ceilingDrainTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	tool.once.Do(func() {
		close(tool.entered)
		select {
		case <-tool.release:
		case <-ctx.Done():
		}
	})
	return tool.truncationEchoTool.Execute(ctx, args)
}

// The oracle is failure-dispatch row 7.2, not the previous regression's manual
// Continue call: the worker must do the draining itself, in a NEW turn at the
// per-agent ceiling, while a below-limit follow-up still needs only one turn.
func TestSteeringCeilingDrain(t *testing.T) {
	for _, tc := range []struct {
		name         string
		limit        int
		followUp     bool
		wantTurns    int
		wantRequests int
		wantNotices  int
		toolRounds   int
		wantReply    bool
	}{
		{"ceiling_with_follow_up", 2, true, 2, 3, 1, 2, true},
		{"below_limit_positive_control", 3, true, 1, 3, 0, 2, true},
		{"ceiling_without_follow_up", 2, false, 1, 2, 1, 2, false},
		// A new input earns one fresh budget, never an unbounded sequence
		// of automatic turns if the model keeps requesting tools.
		{"continuation_hits_ceiling_again", 2, true, 2, 4, 2, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const agentID = "ceiling-drain-agent"
			const followUp = "FOLLOWUP-7K: answer with 7K-HANDLED-OK."
			const reply = "7K-HANDLED-OK"
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("OMNIPUS_HOME", home)
			workspace := filepath.Join(home, "workspace")
			require.NoError(t, os.MkdirAll(workspace, 0o700))
			cfg := &config.Config{Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home: workspace, DefaultModel: config.DefaultModel{Model: "scripted-model"},
					MaxTokens: 4096, MaxToolIterations: 10,
				},
				List: []config.AgentConfig{{ID: agentID, Name: "Ceiling drain", Type: config.AgentTypeCustom,
					Home: workspace, MaxToolIterations: tc.limit}},
			}}
			var steps []truncationScriptStep
			for round := 1; round <= tc.toolRounds; round++ {
				steps = append(steps, truncationScriptStep{finishReason: "tool_calls", toolCalls: []providers.ToolCall{
					truncationEchoCall(fmt.Sprintf("ceiling-call-%d", round), fmt.Sprintf("note-%d", round)),
				}})
			}
			steps = append(steps, truncationScriptStep{content: reply, finishReason: "stop"})
			provider := &truncationScriptedProvider{steps: steps}
			msgBus := bus.NewMessageBus()
			t.Cleanup(msgBus.Close)
			al := mustNewAgentLoop(t, cfg, msgBus, provider)
			t.Cleanup(al.Close)
			inst, ok := al.GetRegistry().GetAgent(agentID)
			require.True(t, ok)
			require.Equal(t, tc.limit, inst.MaxIterations, "test the resolved per-agent value, not a manually edited instance")
			tool := &ceilingDrainTool{entered: make(chan struct{}), release: make(chan struct{})}
			al.RegisterTool(tool)
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"echo_tool": "allow"}})
			store := al.GetSessionStore()
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
			require.NoError(t, err)
			scope := "agent:" + agentID + ":session:" + meta.ID
			msg := bus.InboundMessage{Channel: "webchat", ChatID: "chat-" + meta.ID, SessionID: meta.ID,
				SessionKey: scope, Metadata: map[string]string{"agent_id": agentID}, Content: "Read two notes, one tool at a time."}
			var events []Event
			var eventsMu sync.Mutex
			al.SetEventSyncTap(func(event Event) {
				eventsMu.Lock()
				events = append(events, event)
				eventsMu.Unlock()
			})
			t.Cleanup(func() { al.SetEventSyncTap(nil) })
			worker := newSessionWorker(scope, al, func() {})
			done := make(chan struct{})
			go func() {
				defer close(done)
				worker.processTurn(worker.ctx, msg)
			}()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(tool.release) }) }
			t.Cleanup(func() {
				release()
				worker.cancel()
				<-done
			})
			select {
			case <-tool.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("first tool dispatch never started")
			}
			if tc.followUp {
				late := msg
				late.Content = followUp
				require.True(t, worker.enqueue(late), "follow-up accepted during call 1")
				require.Equal(t, 1, al.pendingSteeringCountForScope(scope), "the follow-up is genuinely in the live turn's queue before call 1 finishes")
			}
			release()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("worker failed to settle its turn and automatic drain")
			}
			assert.Equal(t, int32(tc.toolRounds), tool.calls.Load(), "no tool call is replayed or allowed past the per-turn ceiling")
			assert.Equal(t, tc.wantRequests, provider.CallCount(), "automatic follow-up request, without a manual Continue or resend")
			assert.Zero(t, al.pendingSteeringCountForScope(scope), "worker leaves no stranded input")

			eventsMu.Lock()
			captured := append([]Event(nil), events...)
			eventsMu.Unlock()
			var starts []Event
			var ends []TurnEndPayload
			for _, event := range captured {
				if event.Kind == EventKindTurnStart {
					starts = append(starts, event)
				}
				if event.Kind == EventKindTurnEnd {
					payload, ok := event.Payload.(TurnEndPayload)
					require.True(t, ok, "turn-end events carry their typed iteration record")
					ends = append(ends, payload)
					t.Logf("turn=%s iterations=%d", event.Meta.TurnID, payload.Iterations)
				}
			}
			assert.Len(t, starts, tc.wantTurns, "the ceiling follow-up must run in a fresh turn with its own budget")
			assert.Len(t, ends, tc.wantTurns, "every started turn settles")
			if len(starts) == 2 {
				assert.NotEqual(t, starts[0].Meta.TurnID, starts[1].Meta.TurnID)
			}
			if tc.limit == 2 && len(ends) > 0 {
				assert.Equal(t, 2, ends[0].Iterations, "the original turn stops at exactly two iterations")
			}
			requests := provider.Requests()
			for i, request := range requests {
				t.Logf("request=%d follow_up_occurrences=%d", i+1, countMessagesContaining(request, followUp))
			}
			history := inst.Sessions.GetHistory(scope)
			if tc.followUp {
				assert.Equal(t, 1, countMessagesContaining(history, followUp), "accepted instruction enters model history exactly once")
				var wantAnswers []string
				if tc.wantReply {
					wantAnswers = []string{reply}
				}
				assert.Equal(t, wantAnswers, followUpBoundaryAnswers(history), "only real model answers are saved; a second cutoff cannot renew its own budget")
				if assert.Len(t, requests, tc.wantRequests) {
					assert.Equal(t, 1, countMessagesContaining(requests[len(requests)-1], followUp), "the answer-producing request receives the original instruction exactly once")
				}
			}
			entries, err := store.ReadTranscript(meta.ID)
			require.NoError(t, err)
			var notices []session.TranscriptEntry
			for _, entry := range entries {
				if entry.Content == specToolLimitMessage {
					notices = append(notices, entry)
					assert.Equal(t, session.EntryTypeSystem, entry.Type, "harness notice uses the existing system path")
					assert.Equal(t, "system", entry.Role, "not a model-authored assistant message")
					assert.Empty(t, entry.Model, "the harness must not impersonate the last model")
				}
			}
			assert.Len(t, notices, tc.wantNotices, "exactly one notice per ceiling exit, none below it")
		})
	}
}
