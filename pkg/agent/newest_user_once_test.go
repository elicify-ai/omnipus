// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
					ID: "entry-newest", Role: "user", Content: newest, AgentID: h.agentID, Timestamp: now.Add(2 * time.Second),
				})
			}
			h.append(t, entries...)

			if got := h.ag.Sessions.GetHistory(h.key()); len(got) != 0 {
				t.Fatalf("precondition: in-memory window must be empty, got %+v", got)
			}

			entryID := ""
			if tc.preWritten {
				entryID = "entry-newest"
			}
			_, _, err := h.al.processMessage(context.Background(), bus.InboundMessage{
				Channel:           tc.channel,
				ChatID:            "chat-once",
				SessionID:         h.transcriptID,
				Content:           newest,
				TranscriptEntryID: entryID,
				Sender:            bus.SenderInfo{CanonicalID: tc.channel + ":u1"},
				Metadata:          map[string]string{"agent_id": h.agentID},
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
	id := "entry-" + strings.ReplaceAll(content, " ", "-") + "-" + time.Now().UTC().Format("150405.000000000")
	h.append(t, session.TranscriptEntry{
		ID: id, Role: "user", Content: content, AgentID: h.agentID, Timestamp: time.Now().UTC(),
	})
	runTurnFor(t, h, "webchat", content, id)
}

// runTurnFor runs one turn through processMessage; entryID is the transcript
// entry the inbound path wrote for this message ("" when none was written).
func runTurnFor(t *testing.T, h *hydrateTestHarness, channel, content, entryID string) {
	t.Helper()
	if _, _, err := h.al.processMessage(context.Background(), bus.InboundMessage{
		Channel:           channel,
		ChatID:            "chat-once",
		SessionID:         h.transcriptID,
		Content:           content,
		TranscriptEntryID: entryID,
		Sender:            bus.SenderInfo{CanonicalID: channel + ":u1"},
		Metadata:          map[string]string{"agent_id": h.agentID},
	}); err != nil {
		t.Fatalf("processMessage(%q): %v", content, err)
	}
}

func lastRequest(t *testing.T, rec *userRecordingProvider) []providers.Message {
	t.Helper()
	reqs := rec.snapshot()
	if len(reqs) == 0 {
		t.Fatal("provider received no request")
	}
	return reqs[len(reqs)-1]
}

// (a) The same words sent twice on purpose, the earlier one answered: the
// rebuild must keep the earlier message and the model must also see the
// current one — two "yes" in total, in order.
func TestProcessMessage_SameTextTwice_PreviousAnswered_BothKept(t *testing.T) {
	h, rec := newestOnceHarness(t)
	now := time.Now().UTC()
	h.append(t,
		session.TranscriptEntry{ID: "e1", Role: "user", Content: "yes", AgentID: h.agentID, Timestamp: now},
		session.TranscriptEntry{ID: "e2", Role: "assistant", Content: "answered-first", AgentID: h.agentID, Timestamp: now.Add(time.Second)},
		session.TranscriptEntry{ID: "e3", Role: "user", Content: "yes", AgentID: h.agentID, Timestamp: now.Add(2 * time.Second)},
	)
	runTurnFor(t, h, "webchat", "yes", "e3")
	req := lastRequest(t, rec)
	if n := countUserContent(req, "yes"); n != 2 {
		t.Fatalf("want the earlier and the current \"yes\" (2), got %d; request=%+v", n, req)
	}
	if !strings.Contains(joinContents(req), "answered-first") {
		t.Fatalf("the earlier answer was dropped; request=%+v", req)
	}
}

// (b) The previous identical message is still unanswered and the current one
// arrives on a channel (which records its own entry): both are kept.
func TestProcessMessage_SameTextTwice_PreviousUnanswered_ChannelTurn_BothKept(t *testing.T) {
	h, rec := newestOnceHarness(t)
	h.append(t, session.TranscriptEntry{
		ID: "prev", Role: "user", Content: "ping", AgentID: h.agentID, Timestamp: time.Now().UTC().Add(-time.Minute),
	})
	runTurnFor(t, h, "telegram", "ping", "")
	req := lastRequest(t, rec)
	if n := countUserContent(req, "ping"); n != 2 {
		t.Fatalf("want the unanswered earlier \"ping\" and the current one (2), got %d; request=%+v", n, req)
	}
}

// (c) The current message's transcript write failed (channel path; the write
// is warn-only): no identity exists, so nothing is dropped — the earlier
// identical message stays — and the model still sees the current message once.
func TestProcessMessage_CurrentWriteFailed_NothingDroppedModelSeesCurrentOnce(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX directory permissions enforced for a non-root user")
	}
	h, rec := newestOnceHarness(t)
	h.append(t, session.TranscriptEntry{
		ID: "prev", Role: "user", Content: "ok", AgentID: h.agentID, Timestamp: time.Now().UTC().Add(-time.Minute),
	})
	dir := filepath.Join(os.Getenv("OMNIPUS_HOME"), "sessions", h.transcriptID)
	// Make the session's transcript files read-only so the next append fails.
	var locked []string
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".jsonl") {
			locked = append(locked, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	if len(locked) == 0 {
		t.Fatalf("no transcript file found under %s", dir)
	}
	for _, f := range locked {
		if err := os.Chmod(f, 0o400); err != nil {
			t.Fatalf("chmod %s: %v", f, err)
		}
		t.Cleanup(func() { _ = os.Chmod(f, 0o600) })
	}
	// Instrument check: the write really fails.
	if err := h.store.AppendTranscript(h.transcriptID, session.TranscriptEntry{
		ID: "probe", Role: "user", Content: "probe", AgentID: h.agentID, Timestamp: time.Now().UTC(),
	}); err == nil {
		t.Skip("directory permissions did not make the transcript write fail on this filesystem")
	}
	runTurnFor(t, h, "telegram", "ok", "")
	req := lastRequest(t, rec)
	if n := countUserContent(req, "ok"); n != 2 {
		t.Fatalf("want the earlier \"ok\" kept plus the current one (2), got %d; request=%+v", n, req)
	}
}

// (d) Two queued messages A then B, both already written: at A's turn the
// model sees A once and B not at all; at B's turn it sees both, each once.
func TestProcessMessage_QueuedMessages_NotSentBeforeTheirTurn(t *testing.T) {
	h, rec := newestOnceHarness(t)
	now := time.Now().UTC()
	h.append(t,
		session.TranscriptEntry{ID: "qa", Role: "user", Content: "QUEUED-A-1177", AgentID: h.agentID, Timestamp: now},
		session.TranscriptEntry{ID: "qb", Role: "user", Content: "QUEUED-B-2288", AgentID: h.agentID, Timestamp: now.Add(time.Second)},
	)
	runTurnFor(t, h, "webchat", "QUEUED-A-1177", "qa")
	reqA := lastRequest(t, rec)
	if a, b := countUserContent(reqA, "QUEUED-A-1177"), countUserContent(reqA, "QUEUED-B-2288"); a != 1 || b != 0 {
		t.Fatalf("A's turn: A=%d (want 1), B=%d (want 0); request=%+v", a, b, reqA)
	}
	runTurnFor(t, h, "webchat", "QUEUED-B-2288", "qb")
	reqB := lastRequest(t, rec)
	if a, b := countUserContent(reqB, "QUEUED-A-1177"), countUserContent(reqB, "QUEUED-B-2288"); a != 1 || b != 1 {
		t.Fatalf("B's turn: A=%d (want 1), B=%d (want 1); request=%+v", a, b, reqB)
	}
}

// (e) The persisted entry's text differs from the turn's content (surrounding
// whitespace, an attachment): identity still finds it, so the model sees the
// message once.
func TestProcessMessage_WhitespaceAndAttachmentVariant_SeenOnce(t *testing.T) {
	h, rec := newestOnceHarness(t)
	now := time.Now().UTC()
	h.append(t,
		session.TranscriptEntry{ID: "w0", Role: "user", Content: "earlier", AgentID: h.agentID, Timestamp: now},
		session.TranscriptEntry{ID: "w1", Role: "assistant", Content: "earlier-reply", AgentID: h.agentID, Timestamp: now.Add(time.Second)},
		session.TranscriptEntry{
			ID: "w2", Role: "user", Content: "  PADDED-MARK-6402 \n", AgentID: h.agentID, Timestamp: now.Add(2 * time.Second),
			Attachments: []session.Attachment{{Type: "file", Path: "work/a.txt", Size: 3, MIMEType: "text/plain"}},
		},
	)
	runTurnFor(t, h, "webchat", "PADDED-MARK-6402", "w2")
	req := lastRequest(t, rec)
	if n := countUserContent(req, "PADDED-MARK-6402"); n != 1 {
		t.Fatalf("want the padded message once, got %d; request=%+v", n, req)
	}
}

func joinContents(msgs []providers.Message) string {
	var b strings.Builder
	for i := range msgs {
		b.WriteString(msgs[i].Content)
		b.WriteString("\n")
	}
	return b.String()
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
