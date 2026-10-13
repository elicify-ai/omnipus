package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// attachHarness wires an AgentLoop, the shared transcript store and one
// registered agent the way the turn / re-attach path sees them.
//
// It replaces the deleted attach_hydrate.go harness (ARCHITECT-ANSWER-CUTOVER-
// SLICE4.md Q4): after session-core U2 the store's ONE addressed archive is the
// source of both the chat projection and the model window (DEL-12), so nothing
// rebuilds a separate archive from the transcript, and there is no
// HydrateAgentHistoryFromTranscript / AgentArchiveNonEmpty to drive.
type attachHarness struct {
	al           *AgentLoop
	store        *session.UnifiedStore
	ag           *AgentInstance
	agentID      string
	transcriptID string
}

func (h *attachHarness) key() string {
	return fmt.Sprintf("agent:%s:session:%s", h.agentID, h.transcriptID)
}

// sessionDir is the one archive directory the owning session id names.
func (h *attachHarness) sessionDir() string {
	return filepath.Join(h.store.BaseDir(), h.transcriptID)
}

func newAttachHarness(t *testing.T, agentID string) *attachHarness {
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

	agentCfg := &config.AgentConfig{ID: agentID, Name: "Attach"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if ag == nil {
		t.Fatal("NewAgentInstance returned nil")
	}
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, "Attach")
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return &attachHarness{al: al, store: store, ag: ag, agentID: agentID, transcriptID: meta.ID}
}

func (h *attachHarness) append(t *testing.T, entries ...session.TranscriptEntry) {
	t.Helper()
	for i, e := range entries {
		if err := h.store.AppendTranscript(h.transcriptID, e); err != nil {
			t.Fatalf("AppendTranscript[%d]: %v", i, err)
		}
	}
}

// TestAttach_FreshSessionReadsAnEmptyWindowWithoutWriting — port of
// TestHydrateAgentHistoryFromTranscript_EmptyTranscriptIsNoOp
// (ARCHITECT-ANSWER-CUTOVER-SLICE4.md Q4). Opening a session with no turns
// yields an empty window and an empty chat view, and reading it writes nothing
// (FR-006) — there is no hydration step that could create a file.
func TestAttach_FreshSessionReadsAnEmptyWindowWithoutWriting(t *testing.T) {
	h := newAttachHarness(t, "attach-fresh")

	transcriptPath := filepath.Join(h.sessionDir(), "transcript.jsonl")
	before, err := os.ReadFile(transcriptPath)
	require.NoError(t, err, "a fresh session is born with an (empty) transcript file")
	require.Empty(t, before)

	require.Empty(t, h.ag.Sessions.GetHistory(h.key()), "a session with no turns reads as an empty window")

	entries, err := h.store.ReadTranscript(h.transcriptID)
	require.NoError(t, err)
	require.Empty(t, entries, "a session with no turns has an empty chat view")

	after, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "reading a session must write nothing (FR-006 / Q4)")
}

// TestAttach_OpeningASessionNeverRewritesTheArchive — port of
// TestHydrate_NonEmptyArchiveIsLeftAlone (ARCHITECT-ANSWER-CUTOVER-SLICE4.md
// Q4): opening a session never rewrites retained bytes (FR-006, ADR-066 D5.5).
// The old assertions on the deleted `Hydrated` flag / AgentArchiveNonEmpty are
// dropped with their symbols; the byte-identity assertion is kept, and
// strengthened by reading through a FRESH store over the same directory — a
// real re-attach / restart, not a cached second read.
func TestAttach_OpeningASessionNeverRewritesTheArchive(t *testing.T) {
	h := newAttachHarness(t, "attach-nonempty")

	// A real archive written by the chat path — the same one file set the model
	// view lives in (effects design D1).
	big := strings.Repeat("w", 3000)
	for i := 0; i < 3; i++ {
		h.append(t,
			session.TranscriptEntry{Role: "user", Content: big, AgentID: h.agentID},
			session.TranscriptEntry{Role: "assistant", Content: big, AgentID: h.agentID},
		)
	}

	transcriptPath := filepath.Join(h.sessionDir(), "transcript.jsonl")
	archiveBefore, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	require.NotEmpty(t, archiveBefore, "control: the archive has bytes before the re-open")

	// The re-attach / restart: a FRESH store over the same directory.
	fresh, err := session.NewUnifiedStore(h.store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = fresh.Close() })

	entries, err := fresh.ReadTranscript(h.transcriptID)
	require.NoError(t, err, "opening a session must succeed")
	require.Len(t, entries, 6, "control: the re-open reads the retained chat records")

	archiveAfter, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	require.Equal(t, archiveBefore, archiveAfter,
		"opening a session must leave the archive byte-identical (FR-006 / ADR-066 D5.5)")
}
