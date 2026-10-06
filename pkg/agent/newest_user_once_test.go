// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// userRecordingProvider records every provider request so a test can assert
// what the model was actually sent.
type userRecordingProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
}

func (p *userRecordingProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	return &providers.LLMResponse{Content: "recorded reply"}, nil
}

func (*userRecordingProvider) GetDefaultModel() string { return "test-model" }

func (p *userRecordingProvider) snapshot() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

// countUserContent counts user-role messages whose content contains needle.
func countUserContent(msgs []providers.Message, needle string) int {
	n := 0
	for i := range msgs {
		if msgs[i].Role == "user" && strings.Contains(msgs[i].Content, needle) {
			n++
		}
	}
	return n
}

// TestProcessMessage_NewestUserMessageReachesModelOnce: the user message of
// the current turn is already in the shared transcript when the turn starts
// (the websocket handler and the channel path both write it first). When the
// agent's in-memory window is empty, the self-heal rebuilds the window from
// that transcript. The model must still be sent the newest message exactly
// once.
func TestProcessMessage_NewestUserMessageReachesModelOnce(t *testing.T) {
	const newest = "NEWEST-UNIQUE-MARKER-7431"
	now := time.Now().UTC()

	cases := []struct {
		name    string
		channel string
		prior   bool
		// preWritten: the caller already wrote the newest user entry (the
		// websocket handler does; the channel path writes it itself inside
		// processMessage).
		preWritten bool
	}{
		{"webchat first turn, empty window", "webchat", false, true},
		{"webchat later turn, empty window", "webchat", true, true},
		{"channel later turn, empty window", "telegram", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHydrateTestHarness(t, "once-agent")
			seedTestWorkspaceMembershipForIDs(t, []string{h.agentID})
			rec := &userRecordingProvider{}
			h.ag.mu.Lock()
			h.ag.Provider = rec
			h.ag.Model = "test-model"
			h.ag.mu.Unlock()

			var entries []session.TranscriptEntry
			if tc.prior {
				entries = append(entries,
					session.TranscriptEntry{Role: "user", Content: "earlier question", AgentID: h.agentID, Timestamp: now},
					session.TranscriptEntry{Role: "assistant", Content: "earlier answer", AgentID: h.agentID, Timestamp: now.Add(time.Second)},
				)
			}
			// The inbound path records the newest message before the turn runs.
			if tc.preWritten {
				entries = append(entries, session.TranscriptEntry{
					Role: "user", Content: newest, AgentID: h.agentID, Timestamp: now.Add(2 * time.Second),
				})
			}
			h.append(t, entries...)

			if got := h.ag.Sessions.GetHistory(h.key()); len(got) != 0 {
				t.Fatalf("precondition: in-memory window must be empty, got %+v", got)
			}

			_, _, err := h.al.processMessage(context.Background(), bus.InboundMessage{
				Channel:   tc.channel,
				ChatID:    "chat-once",
				SessionID: h.transcriptID,
				Content:   newest,
				Sender:    bus.SenderInfo{CanonicalID: tc.channel + ":u1"},
				Metadata:  map[string]string{"agent_id": h.agentID},
			})
			if err != nil {
				t.Fatalf("processMessage: %v", err)
			}

			reqs := rec.snapshot()
			if len(reqs) == 0 {
				t.Fatal("provider received no request; the turn did not reach the model")
			}
			if n := countUserContent(reqs[0], newest); n != 1 {
				t.Fatalf("newest user message appears %d times in the first provider request, want exactly 1; request=%+v", n, reqs[0])
			}
		})
	}
}

// newestOnceHarness is a harness whose agent can run real turns against rec.
func newestOnceHarness(t *testing.T) (*hydrateTestHarness, *userRecordingProvider) {
	t.Helper()
	h := newHydrateTestHarness(t, "once-agent")
	seedTestWorkspaceMembershipForIDs(t, []string{h.agentID})
	rec := &userRecordingProvider{}
	h.ag.mu.Lock()
	h.ag.Provider = rec
	h.ag.Model = "test-model"
	h.ag.mu.Unlock()
	return h, rec
}

// sendWebchat mimics the websocket handler: record the user entry, then run
// the turn through processMessage.
func sendWebchat(t *testing.T, h *hydrateTestHarness, content string) {
	t.Helper()
	h.append(t, session.TranscriptEntry{
		Role: "user", Content: content, AgentID: h.agentID, Timestamp: time.Now().UTC(),
	})
	if _, _, err := h.al.processMessage(context.Background(), bus.InboundMessage{
		Channel:   "webchat",
		ChatID:    "chat-once",
		SessionID: h.transcriptID,
		Content:   content,
		Sender:    bus.SenderInfo{CanonicalID: "webchat:u1"},
		Metadata:  map[string]string{"agent_id": h.agentID},
	}); err != nil {
		t.Fatalf("processMessage(%q): %v", content, err)
	}
}

// A turn on a live (non-empty) window and a turn after /clear must also send
// the newest message once.
func TestProcessMessage_NewestUserMessageOnce_LiveWindowAndAfterClear(t *testing.T) {
	h, rec := newestOnceHarness(t)

	sendWebchat(t, h, "first message")
	sendWebchat(t, h, "LIVE-WINDOW-MARKER-5521")
	reqs := rec.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("want 2 provider requests, got %d", len(reqs))
	}
	if n := countUserContent(reqs[1], "LIVE-WINDOW-MARKER-5521"); n != 1 {
		t.Fatalf("live window: newest appears %d times, want 1; request=%+v", n, reqs[1])
	}

	// /clear is answered by the command handler (no provider request).
	sendWebchat(t, h, "/clear")
	if got := len(rec.snapshot()); got != 2 {
		t.Fatalf("/clear must not reach the model; provider requests = %d, want 2", got)
	}
	sendWebchat(t, h, "AFTER-CLEAR-MARKER-9087")
	reqs = rec.snapshot()
	if len(reqs) != 3 {
		t.Fatalf("want 3 provider requests after the post-clear turn, got %d", len(reqs))
	}
	if n := countUserContent(reqs[2], "AFTER-CLEAR-MARKER-9087"); n != 1 {
		t.Fatalf("after /clear: newest appears %d times, want 1; request=%+v", n, reqs[2])
	}
}

// A turn that continues after a restart finds the on-disk transcript and an
// empty in-memory window; the newest message must still be sent once. Two
// turns run, the window is then emptied by dropping the agent's per-session
// state, and a third message is sent.
func TestProcessMessage_NewestUserMessageOnce_AfterRestartEmptyWindow(t *testing.T) {
	h, rec := newestOnceHarness(t)
	sendWebchat(t, h, "turn one")
	h.ag.Sessions.TruncateHistory(h.key(), 0)
	if got := h.ag.Sessions.GetHistory(h.key()); len(got) != 0 {
		t.Fatalf("precondition: window must be empty, got %d messages", len(got))
	}
	sendWebchat(t, h, "RESTART-MARKER-3310")
	reqs := rec.snapshot()
	last := reqs[len(reqs)-1]
	if n := countUserContent(last, "RESTART-MARKER-3310"); n != 1 {
		t.Fatalf("after restart: newest appears %d times, want 1; request=%+v", n, last)
	}
}
