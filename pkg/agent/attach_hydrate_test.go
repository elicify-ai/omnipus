package agent

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestHydrateAgentHistoryFromTranscript_RestoresPriorTurns verifies the bridge
// between the shared transcript store and the per-agent SessionStore. Without
// this hydration, "open past session" only repopulates the SPA UI; the agent's
// in-memory turn buffer stays empty for the new WS chatID and the next LLM
// turn answers as if the session just started.
func TestHydrateAgentHistoryFromTranscript_RestoresPriorTurns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	const agentID = "hydrate-agent"
	agentCfg := &config.AgentConfig{ID: agentID, Name: "Hydrate"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if ag == nil {
		t.Fatal("NewAgentInstance returned nil")
	}
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, "Hydrate")
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	transcriptID := meta.ID

	now := time.Now().UTC()
	for i, e := range []session.TranscriptEntry{
		{Role: "user", Content: "deploy host?", AgentID: agentID, Timestamp: now},
		{Role: "assistant", Content: "prod-east-1.example.com", AgentID: agentID, Timestamp: now.Add(time.Second)},
		{Role: "user", Content: "and the staging one?", AgentID: agentID, Timestamp: now.Add(2 * time.Second)},
		{Role: "assistant", Content: "stage.example.com", AgentID: agentID, Timestamp: now.Add(3 * time.Second)},
	} {
		if err := store.AppendTranscript(transcriptID, e); err != nil {
			t.Fatalf("AppendTranscript[%d]: %v", i, err)
		}
	}

	// Hydrate using only sessionID — the per-agent key is now "agent:<id>:session:<sessionID>".
	if err := al.HydrateAgentHistoryFromTranscript(transcriptID); err != nil {
		t.Fatalf("HydrateAgentHistoryFromTranscript: %v", err)
	}

	wantKey := fmt.Sprintf("agent:%s:session:%s", agentID, transcriptID)
	got := ag.Sessions.GetHistory(wantKey)
	if len(got) != 4 {
		t.Fatalf("hydrated history len = %d, want 4; messages=%+v", len(got), got)
	}
	if got[0].Role != "user" || got[0].Content != "deploy host?" {
		t.Errorf("got[0] = %+v, want user/deploy host?", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "prod-east-1.example.com" {
		t.Errorf("got[1] = %+v, want assistant/prod-east-1.example.com", got[1])
	}
	if got[2].Role != "user" || got[2].Content != "and the staging one?" {
		t.Errorf("got[2] = %+v, want user/and the staging one?", got[2])
	}
	if got[3].Role != "assistant" || got[3].Content != "stage.example.com" {
		t.Errorf("got[3] = %+v, want assistant/stage.example.com", got[3])
	}
}

// TestHydrateAgentHistoryFromTranscript_HandoffBriefReachesTarget confirms
// that a Handoff system entry written by HandoffTool is surfaced to the
// target agent's reconstructed history, so a freshly-handed-off agent sees
// the original brief on its first turn instead of starting blind.
func TestHydrateAgentHistoryFromTranscript_HandoffBriefReachesTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	for _, id := range []string{"mia", "ray"} {
		ag := NewAgentInstance(&config.AgentConfig{ID: id, Name: id},
			&cfg.Agents.Defaults, cfg, &mockProvider{})
		ag.Home = filepath.Join(home, "agents", id)
		ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(id, id)
		al.registry.mu.Lock()
		al.registry.agents[id] = ag
		al.registry.mu.Unlock()
	}

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	now := time.Now().UTC()
	for i, e := range []session.TranscriptEntry{
		{Role: "user", Content: "research agentic ai", AgentID: "mia", Timestamp: now},
		{Role: "assistant", Content: "Connecting you with Ray...", AgentID: "mia", Timestamp: now.Add(time.Second)},
		// Handoff system entry, written by HandoffTool with AgentID = target.
		{
			Type:      session.EntryTypeSystem,
			Role:      "system",
			AgentID:   "ray",
			Content:   "Handoff: mia → Ray. Context: comprehensive overview of agentic AI",
			Timestamp: now.Add(2 * time.Second),
		},
	} {
		if err := store.AppendTranscript(meta.ID, e); err != nil {
			t.Fatalf("AppendTranscript[%d]: %v", i, err)
		}
	}

	if err := al.HydrateAgentHistoryFromTranscript(meta.ID); err != nil {
		t.Fatalf("HydrateAgentHistoryFromTranscript: %v", err)
	}

	rayKey := fmt.Sprintf("agent:ray:session:%s", meta.ID)
	ray, ok := al.GetRegistry().GetAgent("ray")
	if !ok {
		t.Fatal("ray not in registry after setup")
	}
	rayHistory := ray.Sessions.GetHistory(rayKey)
	if len(rayHistory) == 0 {
		t.Fatal("Ray's hydrated history should contain at least the handoff brief")
	}
	hasHandoff := false
	for _, m := range rayHistory {
		if strings.Contains(m.Content, "comprehensive overview of agentic AI") {
			hasHandoff = true
			break
		}
	}
	if !hasHandoff {
		t.Fatalf("Ray did not receive the handoff brief; messages=%+v", rayHistory)
	}
}

// TestHydrateAgentHistoryFromTranscript_EmptyTranscriptIsNoOp confirms the
// helper is safe to call on a brand-new (empty) session — for example when
// the SPA reconnects on a session that hasn't yet exchanged a turn.
func TestHydrateAgentHistoryFromTranscript_EmptyTranscriptIsNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if err := al.HydrateAgentHistoryFromTranscript(meta.ID); err != nil {
		t.Fatalf("HydrateAgentHistoryFromTranscript on empty transcript should not error: %v", err)
	}
}

// TestHydrateAgentHistoryFromTranscript_BlankOwnerFailsLoud is the regression
// guard for the removal of the "main" sentinel's silent fallback. Before the
// sentinel was retired, HydrateAgentHistoryFromTranscript defaulted
// sessionOwner to "main" whenever a transcript entry had a blank AgentID and
// session metadata itself carried no ActiveAgentID/AgentID — so those
// entries hydrated (or silently vanished, since "main" was never really a
// resolvable registry member either) with zero error and zero log signal.
// That was a genuine silent-history-loss path: perAgent[""] would never
// match a real registry.GetAgent lookup, so the affected messages simply
// disappeared from the next turn's context with nothing to indicate why.
//
// The general fallback remains: an entry with no AgentID of its own is
// attributed to the SESSION's owner, which is what lets a session whose
// original agent is gone still hydrate. What was removed is the
// agent-of-last-resort behind it — naming a specific agent when metadata
// resolves nothing would reattribute one agent's history to another.
//
// So when neither the entry nor the session names an owner, the entry is
// simply skipped: not hydrated under a blank owner, and not turned into a
// hard failure of the whole session either. This test pins that the call
// SUCCEEDS and that the unattributable entry reaches no agent.
func TestHydrateAgentHistoryFromTranscript_UnownedEntryIsSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	// Deliberately create the session with an EMPTY creatingAgentID, so its
	// meta carries neither ActiveAgentID nor AgentID — session metadata alone
	// cannot resolve an owner. This mirrors a real (if rare) production
	// gap: a session created via a path that never stamped an owning agent.
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if meta.ActiveAgentID != "" || meta.AgentID != "" {
		t.Fatalf("test setup invariant broken: meta must carry no owner, got ActiveAgentID=%q AgentID=%q",
			meta.ActiveAgentID, meta.AgentID)
	}

	// A transcript entry with a blank AgentID — e.g. assistant text written
	// by the wsStreamer fallback before a handoff ever ran.
	now := time.Now().UTC()
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "assistant",
		Content:   "orphaned reply with no owning agent",
		AgentID:   "",
		Timestamp: now,
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	if err := al.HydrateAgentHistoryFromTranscript(meta.ID); err != nil {
		t.Fatalf("an unattributable entry must be skipped, not fail the session: %v", err)
	}

	// The entry belonged to nobody, so it must have reached nobody. Crucially
	// it must not have landed under a blank key or a guessed identity: the
	// point of removing the sentinel is that no agent inherits another
	// agent's history by default.
	reg := al.GetRegistry()
	for _, id := range reg.ListAgentIDs() {
		ag, ok := reg.GetAgent(id)
		if !ok || ag == nil || ag.Sessions == nil {
			continue
		}
		key := fmt.Sprintf("agent:%s:session:%s", ag.ID, meta.ID)
		for _, m := range ag.Sessions.GetHistory(key) {
			if strings.Contains(m.Content, "orphaned reply with no owning agent") {
				t.Fatalf("agent %q received an entry that named no owner", ag.ID)
			}
		}
	}
}

// hydrateTestHarness wires an AgentLoop, a shared transcript store and one
// registered agent the way the attach path sees them (ADR-066 D5.5 tests).
type hydrateTestHarness struct {
	al           *AgentLoop
	store        *session.UnifiedStore
	ag           *AgentInstance
	agentID      string
	transcriptID string
}

func (h *hydrateTestHarness) key() string {
	return fmt.Sprintf("agent:%s:session:%s", h.agentID, h.transcriptID)
}

// archivePath is the JSONL archive file the agent's UnifiedStore writes for
// this session key (<agent home>/sessions/.context/<sanitised key>.jsonl).
func (h *hydrateTestHarness) archivePath() string {
	name := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(h.key())
	return filepath.Join(h.ag.Home, "sessions", ".context", name+".jsonl")
}

func (h *hydrateTestHarness) metaPath() string {
	return strings.TrimSuffix(h.archivePath(), ".jsonl") + ".meta.json"
}

func newHydrateTestHarness(t *testing.T, agentID string) *hydrateTestHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: agentID, Name: "Hydrate"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if ag == nil {
		t.Fatal("NewAgentInstance returned nil")
	}
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, "Hydrate")
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return &hydrateTestHarness{al: al, store: store, ag: ag, agentID: agentID, transcriptID: meta.ID}
}

func (h *hydrateTestHarness) append(t *testing.T, entries ...session.TranscriptEntry) {
	t.Helper()
	for i, e := range entries {
		if err := h.store.AppendTranscript(h.transcriptID, e); err != nil {
			t.Fatalf("AppendTranscript[%d]: %v", i, err)
		}
	}
}

// standaloneToolCall builds a transcript entry in the REAL shape the turn
// engine writes (pkg/agent/turn.go::appendToolCallTranscript): Type
// "tool_call", no Role, one ToolCall in ToolCalls.
func standaloneToolCall(agentID, turnID, id, tool string, result map[string]any, ts time.Time) session.TranscriptEntry {
	return session.TranscriptEntry{
		ID:        id,
		Type:      session.EntryTypeToolCall,
		AgentID:   agentID,
		TurnID:    turnID,
		Timestamp: ts,
		ToolCalls: []session.ToolCall{{
			ID:         session.ToolCallID(id),
			Tool:       tool,
			Status:     "success",
			Parameters: map[string]any{"q": id},
			Result:     result,
		}},
	}
}

// TestAttach_EmptyArchiveHydratesStandaloneToolCalls — ADR-066 D5.5, FR-046,
// B-53 / B-53b, DS-10 #6, #6b, #7 (test 56).
//
// The fixture uses the real transcript shape: assistant entries carry NO
// tool_calls; every tool call is a standalone `type: "tool_call"` entry. The
// old hydration matched neither case for those entries and silently dropped
// every tool call (the verified operator bug). After hydration the archive
// must hold exactly one role:"tool" line per recorded call, each attached as
// a ToolCall to the preceding assistant message of its turn, and meta must
// carry hydrated: true.
func TestAttach_EmptyArchiveHydratesStandaloneToolCalls(t *testing.T) {
	t.Run("DS-10 #6: calls attach to the preceding assistant message of their turn", func(t *testing.T) {
		h := newHydrateTestHarness(t, "hydrate-standalone")
		now := time.Now().UTC()
		h.append(t,
			session.TranscriptEntry{Role: "user", Content: "first ask", AgentID: h.agentID, TurnID: "A", Timestamp: now},
			session.TranscriptEntry{Role: "assistant", Content: "looking", AgentID: h.agentID, TurnID: "A", Timestamp: now.Add(time.Second)},
			standaloneToolCall(h.agentID, "A", "call-a1", "read_file", map[string]any{"content": "alpha"}, now.Add(2*time.Second)),
			standaloneToolCall(h.agentID, "A", "call-a2", "bash", map[string]any{"output": "beta"}, now.Add(3*time.Second)),
			session.TranscriptEntry{Role: "assistant", Content: "done with A", AgentID: h.agentID, TurnID: "A", Timestamp: now.Add(4 * time.Second)},
			session.TranscriptEntry{Role: "user", Content: "second ask", AgentID: h.agentID, TurnID: "B", Timestamp: now.Add(5 * time.Second)},
			session.TranscriptEntry{Role: "assistant", Content: "checking", AgentID: h.agentID, TurnID: "B", Timestamp: now.Add(6 * time.Second)},
			standaloneToolCall(h.agentID, "B", "call-b1", "web_fetch", map[string]any{"body": "gamma"}, now.Add(7*time.Second)),
			session.TranscriptEntry{Role: "assistant", Content: "done with B", AgentID: h.agentID, TurnID: "B", Timestamp: now.Add(8 * time.Second)},
		)

		if err := h.al.HydrateAgentHistoryFromTranscript(h.transcriptID); err != nil {
			t.Fatalf("HydrateAgentHistoryFromTranscript: %v", err)
		}

		got := h.ag.Sessions.GetHistory(h.key())
		// user, assistant(+2 calls), tool, tool, assistant, user, assistant(+1 call), tool, assistant
		wantRoles := []string{"user", "assistant", "tool", "tool", "assistant", "user", "assistant", "tool", "assistant"}
		if len(got) != len(wantRoles) {
			t.Fatalf("hydrated history len = %d, want %d; messages=%+v", len(got), len(wantRoles), got)
		}
		for i, r := range wantRoles {
			if got[i].Role != r {
				t.Errorf("got[%d].Role = %q, want %q (%+v)", i, got[i].Role, r, got[i])
			}
		}
		toolLines := 0
		for _, m := range got {
			if m.Role == "tool" {
				toolLines++
			}
		}
		if toolLines != 3 {
			t.Errorf("role:tool lines = %d, want exactly 3 (one per recorded call)", toolLines)
		}

		// A's two calls on A's "looking" message, in transcript order.
		a := got[1]
		if a.Content != "looking" || len(a.ToolCalls) != 2 {
			t.Fatalf("turn A assistant = %+v, want content 'looking' with 2 tool calls", a)
		}
		if a.ToolCalls[0].ID != "call-a1" || a.ToolCalls[0].Function == nil || a.ToolCalls[0].Function.Name != "read_file" {
			t.Errorf("A.ToolCalls[0] = %+v, want call-a1/read_file", a.ToolCalls[0])
		}
		if a.ToolCalls[1].ID != "call-a2" || a.ToolCalls[1].Function == nil || a.ToolCalls[1].Function.Name != "bash" {
			t.Errorf("A.ToolCalls[1] = %+v, want call-a2/bash", a.ToolCalls[1])
		}
		if got[2].ToolCallID != "call-a1" || !strings.Contains(got[2].Content, "alpha") {
			t.Errorf("got[2] = %+v, want tool result for call-a1 carrying 'alpha'", got[2])
		}
		if got[3].ToolCallID != "call-a2" || !strings.Contains(got[3].Content, "beta") {
			t.Errorf("got[3] = %+v, want tool result for call-a2 carrying 'beta'", got[3])
		}
		// A's final assistant message carries no calls; B's "checking" carries B's.
		if len(got[4].ToolCalls) != 0 {
			t.Errorf("got[4] (A final) must carry no tool calls, got %+v", got[4].ToolCalls)
		}
		b := got[6]
		if b.Content != "checking" || len(b.ToolCalls) != 1 || b.ToolCalls[0].ID != "call-b1" {
			t.Fatalf("turn B assistant = %+v, want content 'checking' with call-b1", b)
		}
		if got[7].ToolCallID != "call-b1" || !strings.Contains(got[7].Content, "gamma") {
			t.Errorf("got[7] = %+v, want tool result for call-b1 carrying 'gamma'", got[7])
		}

		// FR-046 / B-53b: a rebuilt archive is flagged hydrated.
		if !h.ag.Sessions.Projection(h.key()).Hydrated {
			t.Errorf("Projection(%q).Hydrated = false, want true after hydration", h.key())
		}
		archived, err := h.ag.Sessions.ReadArchive(context.Background(), h.key())
		if err != nil {
			t.Fatalf("ReadArchive: %v", err)
		}
		if len(archived) != len(wantRoles) {
			t.Errorf("archive lines = %d, want %d", len(archived), len(wantRoles))
		}
	})

	t.Run("DS-10 #6b: no preceding assistant entry in the turn → synthetic assistant message", func(t *testing.T) {
		h := newHydrateTestHarness(t, "hydrate-synthetic")
		now := time.Now().UTC()
		h.append(t,
			session.TranscriptEntry{Role: "user", Content: "previous ask", AgentID: h.agentID, TurnID: "P", Timestamp: now},
			session.TranscriptEntry{Role: "assistant", Content: "previous answer", AgentID: h.agentID, TurnID: "P", Timestamp: now.Add(time.Second)},
			session.TranscriptEntry{Role: "user", Content: "do it", AgentID: h.agentID, TurnID: "Q", Timestamp: now.Add(2 * time.Second)},
			// The model emitted only a tool call — no narration text, so no
			// assistant entry precedes the call inside turn Q.
			standaloneToolCall(h.agentID, "Q", "call-q1", "bash", map[string]any{"output": "delta"}, now.Add(3*time.Second)),
			session.TranscriptEntry{Role: "assistant", Content: "did it", AgentID: h.agentID, TurnID: "Q", Timestamp: now.Add(4 * time.Second)},
		)

		if err := h.al.HydrateAgentHistoryFromTranscript(h.transcriptID); err != nil {
			t.Fatalf("HydrateAgentHistoryFromTranscript: %v", err)
		}
		got := h.ag.Sessions.GetHistory(h.key())
		wantRoles := []string{"user", "assistant", "user", "assistant", "tool", "assistant"}
		if len(got) != len(wantRoles) {
			t.Fatalf("hydrated history len = %d, want %d; messages=%+v", len(got), len(wantRoles), got)
		}
		for i, r := range wantRoles {
			if got[i].Role != r {
				t.Errorf("got[%d].Role = %q, want %q", i, got[i].Role, r)
			}
		}
		// The call must NOT be attached to the previous turn's assistant message.
		if len(got[1].ToolCalls) != 0 {
			t.Errorf("previous-turn assistant must carry no calls, got %+v", got[1].ToolCalls)
		}
		syn := got[3]
		if syn.Content != "" || len(syn.ToolCalls) != 1 || syn.ToolCalls[0].ID != "call-q1" {
			t.Fatalf("synthetic assistant = %+v, want empty content with call-q1", syn)
		}
		if got[4].ToolCallID != "call-q1" || !strings.Contains(got[4].Content, "delta") {
			t.Errorf("got[4] = %+v, want tool result for call-q1 carrying 'delta'", got[4])
		}
	})

	t.Run("DS-10 #7: result absent (pre-field transcript) → status-shaped tool line, hydrated: true", func(t *testing.T) {
		h := newHydrateTestHarness(t, "hydrate-prefield")
		now := time.Now().UTC()
		h.append(t,
			session.TranscriptEntry{Role: "user", Content: "ask", AgentID: h.agentID, TurnID: "R", Timestamp: now},
			session.TranscriptEntry{Role: "assistant", Content: "on it", AgentID: h.agentID, TurnID: "R", Timestamp: now.Add(time.Second)},
			standaloneToolCall(h.agentID, "R", "call-r1", "bash", nil, now.Add(2*time.Second)),
		)
		if err := h.al.HydrateAgentHistoryFromTranscript(h.transcriptID); err != nil {
			t.Fatalf("HydrateAgentHistoryFromTranscript: %v", err)
		}
		got := h.ag.Sessions.GetHistory(h.key())
		if len(got) != 3 || got[2].Role != "tool" || got[2].ToolCallID != "call-r1" {
			t.Fatalf("hydrated history = %+v, want user/assistant/tool(call-r1)", got)
		}
		if !strings.Contains(got[2].Content, `"status":"success"`) {
			t.Errorf("pre-field tool line content = %q, want the status fallback", got[2].Content)
		}
		if !h.ag.Sessions.Projection(h.key()).Hydrated {
			t.Errorf("hydrated flag must be set on a transcript-rebuilt archive")
		}
	})

	t.Run("duplicate tool_call entries with one id yield one tool line", func(t *testing.T) {
		// appendToolCallTranscript may fall through to a second append for
		// the same id when the approval-gate placeholder replacement fails;
		// FR-046 still wants exactly one tool line per recorded call.
		h := newHydrateTestHarness(t, "hydrate-dup")
		now := time.Now().UTC()
		pending := standaloneToolCall(h.agentID, "S", "call-s1", "bash", nil, now.Add(2*time.Second))
		pending.ToolCalls[0].Status = "pending"
		h.append(t,
			session.TranscriptEntry{Role: "user", Content: "ask", AgentID: h.agentID, TurnID: "S", Timestamp: now},
			session.TranscriptEntry{Role: "assistant", Content: "on it", AgentID: h.agentID, TurnID: "S", Timestamp: now.Add(time.Second)},
			pending,
			standaloneToolCall(h.agentID, "S", "call-s1", "bash", map[string]any{"output": "settled"}, now.Add(3*time.Second)),
		)
		if err := h.al.HydrateAgentHistoryFromTranscript(h.transcriptID); err != nil {
			t.Fatalf("HydrateAgentHistoryFromTranscript: %v", err)
		}
		got := h.ag.Sessions.GetHistory(h.key())
		if len(got) != 3 {
			t.Fatalf("hydrated history len = %d, want 3; %+v", len(got), got)
		}
		if len(got[1].ToolCalls) != 1 {
			t.Errorf("assistant carries %d calls, want 1", len(got[1].ToolCalls))
		}
		if !strings.Contains(got[2].Content, "settled") {
			t.Errorf("tool line = %q, want the settled (last) record", got[2].Content)
		}
	})
}

// TestHydrate_NonEmptyArchiveIsLeftAlone — ADR-066 FR-045 at the hydration
// function itself: an agent whose archive already has ≥ 1 line is skipped
// (bytes, skip and the hydrated flag untouched), and AgentArchiveNonEmpty —
// the attach path's pre-check — reports it.
func TestHydrate_NonEmptyArchiveIsLeftAlone(t *testing.T) {
	h := newHydrateTestHarness(t, "hydrate-nonempty")
	now := time.Now().UTC()
	h.append(t,
		session.TranscriptEntry{Role: "user", Content: "ask", AgentID: h.agentID, TurnID: "T", Timestamp: now},
		session.TranscriptEntry{Role: "assistant", Content: "answer", AgentID: h.agentID, TurnID: "T", Timestamp: now.Add(time.Second)},
	)
	if h.al.AgentArchiveNonEmpty(h.transcriptID) {
		t.Fatal("AgentArchiveNonEmpty must be false before any archive line exists")
	}

	// A real archive written by the turn path, not by hydration.
	for i := 0; i < 6; i++ {
		h.ag.Sessions.AddMessage(h.key(), "user", fmt.Sprintf("live user %d", i))
		h.ag.Sessions.AddMessage(h.key(), "assistant", fmt.Sprintf("live assistant %d", i))
	}
	h.ag.Sessions.TruncateHistory(h.key(), 4) // skip = 8
	if err := h.ag.Sessions.Save(h.key()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	before, err := os.ReadFile(h.archivePath())
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	metaBefore, err := os.ReadFile(h.metaPath())
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}

	if !h.al.AgentArchiveNonEmpty(h.transcriptID) {
		t.Fatal("AgentArchiveNonEmpty must be true once the archive has lines")
	}
	if hErr := h.al.HydrateAgentHistoryFromTranscript(h.transcriptID); hErr != nil {
		t.Fatalf("HydrateAgentHistoryFromTranscript: %v", hErr)
	}
	after, err := os.ReadFile(h.archivePath())
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("archive bytes changed by hydration on a non-empty archive")
	}
	metaAfter, err := os.ReadFile(h.metaPath())
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if string(metaBefore) != string(metaAfter) {
		t.Errorf("meta changed by hydration on a non-empty archive:\n%s\n---\n%s", metaBefore, metaAfter)
	}
	if h.ag.Sessions.Projection(h.key()).Hydrated {
		t.Errorf("hydrated flag must not be set when hydration was skipped")
	}
	if n := len(h.ag.Sessions.GetHistory(h.key())); n != 4 {
		t.Errorf("window len = %d, want 4 (skip untouched)", n)
	}
}

// TestHydrateAgentHistoryFromTranscript_UnreadableArchiveIsNotFlaggedHydrated
// is the regression for the "flag a live session hydrated on a write that
// never landed" bug.
//
// agentArchiveHasLines used to treat a ReadArchive FAILURE as "empty", so an
// archive that is temporarily unreadable — e.g. it carries an over-long line
// that trips bufio.Scanner — passed the FR-045 guard. SetHistory then
// correctly refused it (ErrArchiveNotEmpty), but SessionWriter.SetHistory is
// fire-and-forget, so the refusal was swallowed and the unconditional
// MarkHydrated below it set the ONE-WAY hydrated flag on the real, live
// archive. From then on every recall_conversation(tool_call_id) answered
// "session was rebuilt from the transcript", turning every [capped]/[emptied]
// mark in that session into a dead pointer — while the operator log reported
// hydration succeeded.
func TestHydrateAgentHistoryFromTranscript_UnreadableArchiveIsNotFlaggedHydrated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	sessionsDir := filepath.Join(home, "sessions")
	store, err := session.NewUnifiedStore(sessionsDir)
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	const agentID = "mia"
	ag := NewAgentInstance(&config.AgentConfig{ID: agentID, Name: agentID},
		&cfg.Agents.Defaults, cfg, &mockProvider{})
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, agentID)
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	now := time.Now().UTC()
	for i, e := range []session.TranscriptEntry{
		{Role: "user", Content: "deploy host?", AgentID: agentID, Timestamp: now},
		{Role: "assistant", Content: "prod-east-1", AgentID: agentID, Timestamp: now.Add(time.Second)},
	} {
		if err := store.AppendTranscript(meta.ID, e); err != nil {
			t.Fatalf("AppendTranscript[%d]: %v", i, err)
		}
	}

	// A REAL, non-empty archive that cannot be read: one line above
	// bufio.Scanner's 10 MB ceiling. Seed one message through the agent's own
	// store first so the archive file exists where that store actually keeps
	// it (a per-agent store under the agent workspace, not `sessionsDir`),
	// then locate it by its sanitized name (':' → '_') and overwrite it.
	key := fmt.Sprintf("agent:%s:session:%s", agentID, meta.ID)
	ag.Sessions.AddMessage(key, "user", "seed")
	wantName := strings.ReplaceAll(key, ":", "_") + ".jsonl"
	archivePath := ""
	if walkErr := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // best-effort search
		}
		if d.Name() == wantName {
			archivePath = path
		}
		return nil
	}); walkErr != nil {
		t.Fatalf("locate archive: %v", walkErr)
	}
	if archivePath == "" {
		t.Fatalf("could not locate the agent archive %q under %s", wantName, home)
	}
	oversize := make([]byte, 11*1024*1024)
	for i := range oversize {
		oversize[i] = 'x'
	}
	line := append([]byte(`{"role":"user","content":"`), oversize...)
	line = append(line, []byte(`"}`+"\n")...)
	if err := os.WriteFile(archivePath, line, 0o600); err != nil {
		t.Fatalf("write oversize archive: %v", err)
	}
	if _, readErr := ag.Sessions.ReadArchive(t.Context(), key); readErr == nil {
		t.Fatal("precondition: the archive must be unreadable for this test to mean anything")
	}

	if err := al.HydrateAgentHistoryFromTranscript(meta.ID); err != nil {
		t.Fatalf("HydrateAgentHistoryFromTranscript: %v", err)
	}

	if ag.Sessions.Projection(key).Hydrated {
		t.Fatal("an archive that could not be read must never be flagged hydrated: the flag is " +
			"one-way and permanently disables recall by tool_call_id for the whole session")
	}
}

// raceyFakeSessionStore is a minimal session.SessionStore fake purpose-built
// to reproduce M5 deterministically, without depending on real goroutine
// timing (which false-green-patterns.md flags as an unreliable proof — see
// "A stopwatch is not a proof of logic"). It simulates the EXACT race the
// finding describes: hydration's own FR-045 pre-check (a real ReadArchive
// call, via agentArchiveHasLines) sees the archive genuinely empty, but by
// the time SetHistory executes, a "foreign" write — standing in for the
// session's own first live turn appending its own user message, running on
// another goroutine — has already landed. Real SessionWriter.SetHistory is
// fire-and-forget and silently refuses (ErrArchiveNotEmpty, swallowed) once
// that has happened; this fake reproduces exactly that effect by writing the
// foreign content INSIDE SetHistory itself, at the one moment production
// code cannot observe it happening.
type raceyFakeSessionStore struct {
	archive        []providers.Message
	foreignContent []providers.Message // injected the moment SetHistory is called
	hydrated       bool
	setHistoryCall int
}

func (f *raceyFakeSessionStore) GetHistory(string) []providers.Message { return f.archive }

func (f *raceyFakeSessionStore) ReadArchive(context.Context, string) ([]memory.ArchivedMessage, error) {
	out := make([]memory.ArchivedMessage, len(f.archive))
	for i, m := range f.archive {
		out[i] = memory.ArchivedMessage{Message: m}
	}
	return out, nil
}

func (f *raceyFakeSessionStore) Projection(string) memory.ProjectionMeta {
	return memory.ProjectionMeta{Hydrated: f.hydrated}
}

func (f *raceyFakeSessionStore) AddMessage(_, role, content string) {
	f.archive = append(f.archive, providers.Message{Role: role, Content: content})
}
func (f *raceyFakeSessionStore) AddFullMessage(_ string, msg providers.Message) {
	f.archive = append(f.archive, msg)
}

// SetHistory reproduces SessionWriter's real contract: it fills only an
// EMPTY archive and is fire-and-forget (no error surfaces to the caller).
// The race: between the caller's own pre-check and THIS call, foreignContent
// (if set) is simulated as having landed first — so the write this call
// actually intended (history) is REFUSED, exactly like the real
// ErrArchiveNotEmpty path, and the caller has no way to tell from the return
// value.
func (f *raceyFakeSessionStore) SetHistory(_ string, history []providers.Message) {
	f.setHistoryCall++
	if len(f.foreignContent) > 0 {
		f.archive = f.foreignContent // the race: someone else's write wins
		return
	}
	if len(f.archive) > 0 {
		return // real ErrArchiveNotEmpty refusal — a no-op, swallowed
	}
	f.archive = history
}

func (f *raceyFakeSessionStore) TruncateHistory(string, int)                             {}
func (f *raceyFakeSessionStore) RollbackAppended(string, int, int, memory.ProjectionSet) {}
func (f *raceyFakeSessionStore) SetProjectionState(string, memory.ProjectionKey, memory.ProjectionState) {
}
func (f *raceyFakeSessionStore) MarkHydrated(string) { f.hydrated = true }
func (f *raceyFakeSessionStore) Save(string) error   { return nil }
func (f *raceyFakeSessionStore) Close() error        { return nil }

var _ session.SessionStore = (*raceyFakeSessionStore)(nil)

// TestHydrateOneAgent_ForeignWriteRacesIn_NotMarkedHydrated — M5: a live
// turn's own first append can land between HydrateAgentHistoryFromTranscript's
// FR-045 empty check and its SetHistory call. SetHistory is fire-and-forget,
// so hydration has no direct signal that ITS write was refused. Before this
// fix, the post-write check was "is the archive non-empty now" — which the
// foreign write alone satisfies — so the session got permanently
// MarkHydrated'd on the strength of someone ELSE's write. Every later
// recall_conversation(tool_call_id) on that session then answers "session
// was rebuilt from the transcript" — turning every real [capped]/[emptied]
// mark into a dead pointer — while the operator log claims hydration
// succeeded.
func TestHydrateOneAgent_ForeignWriteRacesIn_NotMarkedHydrated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	const agentID = "mia"
	ag := NewAgentInstance(&config.AgentConfig{ID: agentID, Name: agentID}, &cfg.Agents.Defaults, cfg, &mockProvider{})
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, agentID)

	fake := &raceyFakeSessionStore{
		// The live turn's own first message, standing in for whatever landed
		// on another goroutine between the check and the write.
		foreignContent: []providers.Message{{Role: "user", Content: "hey, are you there?"}},
	}
	ag.Sessions = fake

	hydrationPayload := []providers.Message{
		{Role: "user", Content: "deploy host?"},
		{Role: "assistant", Content: "prod-east-1"},
	}
	al.hydrateOneAgent(ag, agentID, "sess-race", hydrateSessionKey(agentID, "sess-race"), hydrationPayload)

	require.Equal(t, 1, fake.setHistoryCall, "the write must still be attempted")
	assert.False(t, fake.hydrated,
		"M5: a foreign write raced in — this must NOT be marked hydrated, or recall_conversation "+
			"permanently answers 'session was rebuilt' for a session that is actually live")
	assert.Equal(t, fake.foreignContent, fake.archive,
		"the foreign content must be left exactly as it was — hydration must not clobber a live write")
}

// TestArchiveMatchesHydratedMessages — M5: the pure verification predicate.
// The old post-write check was `len(archived) > 0` — true for ANY write by
// ANY actor. archiveMatchesHydratedMessages must distinguish "my hydration
// payload landed" from "someone else's write landed instead", even though
// both leave a non-empty archive.
func TestArchiveMatchesHydratedMessages(t *testing.T) {
	payload := []providers.Message{
		{Role: "user", Content: "deploy host?"},
		{Role: "assistant", Content: "prod-east-1"},
	}

	t.Run("exact match: my own write landed", func(t *testing.T) {
		archived := []memory.ArchivedMessage{
			{Message: providers.Message{Role: "user", Content: "deploy host?"}},
			{Message: providers.Message{Role: "assistant", Content: "prod-east-1"}},
		}
		assert.True(t, archiveMatchesHydratedMessages(archived, payload))
	})

	t.Run("M5: a foreign write — the old bare non-empty check would be fooled", func(t *testing.T) {
		foreign := []memory.ArchivedMessage{
			{Message: providers.Message{Role: "user", Content: "hey, are you there?"}},
		}
		oldCheckWouldSayWrote := len(foreign) > 0
		require.True(t, oldCheckWouldSayWrote, "precondition: the old check is fooled by this exact input")
		assert.False(t, archiveMatchesHydratedMessages(foreign, payload),
			"a foreign write must never be mistaken for the hydration payload landing")
	})

	t.Run("length mismatch alone refuses", func(t *testing.T) {
		archived := []memory.ArchivedMessage{{Message: providers.Message{Role: "user", Content: "deploy host?"}}}
		assert.False(t, archiveMatchesHydratedMessages(archived, payload))
	})

	t.Run("same length, different tool_call_id refuses", func(t *testing.T) {
		archived := []memory.ArchivedMessage{
			{Message: providers.Message{Role: "tool", Content: "result", ToolCallID: "call_other"}},
		}
		got := []providers.Message{{Role: "tool", Content: "result", ToolCallID: "call_mine"}}
		assert.False(t, archiveMatchesHydratedMessages(archived, got))
	})

	t.Run("empty payload matches empty archive", func(t *testing.T) {
		assert.True(t, archiveMatchesHydratedMessages(nil, nil))
	})
}
