package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/memrooms"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Founder Q1 B, 2026-10-21: conversations cannot move between workspaces.
// Changing a channel's binding selects a different conversation, not old history.
func TestMemoryGateRound2(t *testing.T) {
	t.Run("ImmutableSessionWorkspace", gateImmutableSessionWorkspace)
	t.Run("FreshChannelHistory", gateFreshChannelHistory)
	t.Run("HelperWorkspace", gateHelperWorkspace)
	t.Run("ScanError", gateMemoryScanError)
	t.Run("CitationIOError", gateCitationIOError)
	t.Run("RenderScopeMismatch", gateRenderScopeMismatch)
	t.Run("HelperRefusalText", func(t *testing.T) {
		f := newTurnMemoryFixture(t)
		meta, _ := f.chat(t, "alpha")
		al := &AgentLoop{sharedSessionStore: f.sessions}
		err := al.validateInboundWorkspace(bus.InboundMessage{SessionID: meta.ID, Channel: "webchat", Metadata: map[string]string{"workspace_id": "beta"}})
		if err == nil {
			t.Fatal("foreign helper input was accepted")
		}
		visible := userVisibleTurnError(err)
		if !strings.Contains(visible, "workspace") || !strings.Contains(visible, "conversation") {
			t.Errorf("helper workspace refusal lost its clear message: %q", visible)
		}
	})
}

func gateImmutableSessionWorkspace(t *testing.T) {
	f := newTurnMemoryFixture(t)
	t.Run("UsedUnfiledCannotBeAdopted", func(t *testing.T) {
		meta, err := f.sessions.NewSession(session.SessionTypeChat, "webchat", "self")
		if err != nil {
			t.Fatal(err)
		}
		if actionErr := f.sessions.AddMessage(meta.ID, "user", "unfiled history"); actionErr != nil {
			t.Fatal(actionErr)
		}
		ws := "alpha"
		if err := f.sessions.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws}); err == nil {
			t.Error("used unfiled conversation was silently adopted")
		}
	})
	for _, kind := range []session.UnifiedSessionType{session.SessionTypeChat, session.SessionTypeChannel, session.SessionTypeDelegate} {
		t.Run(string(kind), func(t *testing.T) {
			meta, err := f.sessions.NewSession(kind, "webchat", "self")
			if err != nil {
				t.Fatal(err)
			}
			a, b := "alpha", "beta"
			if actionErr := f.sessions.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &a}); actionErr != nil {
				t.Fatal(actionErr)
			}
			if actionErr := f.sessions.AddMessage(meta.ID, "user", "ROOM_ALPHA_RECALLED"); actionErr != nil {
				t.Fatal(actionErr)
			}
			before, err := f.sessions.ReadArchive(t.Context(), meta.ID)
			if err != nil || len(before) != 1 {
				t.Fatalf("A archive positive control: %v %v", before, err)
			}
			for _, other := range []string{b, ""} {
				if scopeErr := f.sessions.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &other}); scopeErr == nil {
					t.Errorf("existing %s accepted workspace change from A to %q", kind, other)
				}
			}
			after, err := f.sessions.GetMeta(meta.ID)
			if err != nil || after.WorkspaceID != a {
				t.Errorf("A identity changed: %+v err=%v", after, err)
			}
			archive, err := f.sessions.ReadArchive(t.Context(), meta.ID)
			if err != nil || len(archive) != 1 || archive[0].Content != "ROOM_ALPHA_RECALLED" {
				t.Errorf("A archive lost: %+v err=%v", archive, err)
			}
		})
	}
}

func gateFreshChannelHistory(t *testing.T) {
	f := newTurnMemoryFixture(t)
	al := &AgentLoop{sharedSessionStore: f.sessions}
	_, ctxA := f.chat(t, "alpha")
	f.save(t, ctxA, "ROOM_ALPHA_SHARED", "shared")
	_, ctxB := f.chat(t, "beta")
	f.save(t, ctxB, "ROOM_BETA_SHARED", "shared")
	a := al.resolveOrCreateChannelSession("telegram", "telegram.sales", "peer", "self", "Peer", "alpha")
	if a == "" {
		t.Fatal("A channel session not created")
	}
	recalled := f.recall.Execute(ctxA, map[string]any{"query": "roomnote", "room": "shared"})
	if recalled.IsError {
		t.Fatal(recalled.ForLLM)
	}
	requireTurnMemoryNotes(t, recalled.ForLLM, "ROOM_ALPHA_SHARED")
	if actionErr := f.sessions.AddMessage(a, "user", "recall A"); actionErr != nil {
		t.Fatal(actionErr)
	}
	if actionErr := f.sessions.AddMessage(a, "assistant", recalled.ForLLM); actionErr != nil {
		t.Fatal(actionErr)
	}
	b := al.resolveOrCreateChannelSession("telegram", "telegram.sales", "peer", "self", "Peer", "beta")
	if b == "" || b == a {
		t.Errorf("channel rebind reused A: A=%q B=%q", a, b)
	}
	metaB, err := f.sessions.GetMeta(b)
	if err != nil || metaB.WorkspaceID != "beta" {
		t.Fatalf("B identity: %+v err=%v", metaB, err)
	}
	ag := &AgentInstance{ID: "self", ContextBuilder: f.cb, Sessions: f.sessions}
	ts := &turnState{agent: ag, sessionKey: b, transcriptSessionID: b, opts: processOptions{WorkspaceID: "beta", TranscriptSessionID: b}}
	messages := al.assembleMessages(t.Context(), ts, nil, "work in B", nil, nil)
	for _, message := range messages {
		if strings.Contains(message.Content, "ROOM_ALPHA_SHARED") {
			t.Error("A recalled note reached B model history")
		}
	}
	requireTurnMemoryNotes(t, messages[0].Content, "ROOM_BETA_SHARED")
	result := f.recall.Execute(ctxB, map[string]any{"query": "roomnote", "room": "shared"})
	if result.IsError {
		t.Fatal(result.ForLLM)
	}
	requireTurnMemoryNotes(t, result.ForLLM, "ROOM_BETA_SHARED")
	old, err := f.sessions.ReadArchive(t.Context(), a)
	if err != nil || len(old) != 2 || !strings.Contains(old[1].Content, "ROOM_ALPHA_SHARED") {
		t.Errorf("A archive no longer readable: %+v %v", old, err)
	}
	if got := al.resolveOrCreateChannelSession("telegram", "telegram.sales", "peer", "self", "Peer", "beta"); got != b {
		t.Errorf("stable B duplicated: got %q want %q", got, b)
	}
	al.channelSessionIdx = sync.Map{}
	al.rebuildChannelSessionIndex()
	if got := al.resolveOrCreateChannelSession("telegram", "telegram.sales", "peer", "self", "Peer", "beta"); got != b {
		t.Errorf("restart did not restore B: got %q want %q", got, b)
	}
}

func gateHelperWorkspace(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	store := al.GetSessionStore()
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("real helper agent missing")
	}
	ms := inst.ContextBuilder.Memory()
	for ws, body := range map[string]string{"alpha": "ROOM_ALPHA_SHARED", "beta": "ROOM_BETA_SHARED"} {
		if actionErr := ms.AppendLongTermToScope(body, "reference", memrooms.RoomScopeShared, ms.scopeForWorkspace(ws)); actionErr != nil {
			t.Fatal(actionErr)
		}
	}
	launch := func(ws string) string {
		t.Helper()
		parent := newTestSteeringSession(t, al, ws)
		child, err := NewSteerLauncher(al).Launch(t.Context(), steer.LaunchRequest{SteeringSessionID: parent, TargetAgentID: testDefaultAgentID, Task: "inspect own workspace", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "round2-" + ws}})
		if err != nil {
			t.Fatal(err)
		}
		return child.SessionID
	}
	a := launch("alpha")
	bws := "beta"
	if err := store.SetMeta(a, session.MetaPatch{WorkspaceID: &bws}); err == nil {
		t.Error("helper accepted cross-workspace reuse")
	}
	b := launch("beta")
	rec, err := al.GetSessionLifecycleStore().Load(b)
	if err != nil {
		t.Fatal(err)
	}
	stale := *rec
	stale.WorkspaceID = "alpha"
	if _, staleErr := al.reconstructSteeredTurn(&stale, nil); staleErr == nil {
		t.Error("helper accepted a lifecycle workspace that disagrees with its session")
	}
	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil || ts.opts.WorkspaceID != "beta" {
		t.Fatalf("new B helper scope: %+v %v", ts, err)
	}
	messages := al.assembleMessages(t.Context(), ts, nil, ts.opts.UserMessage, nil, nil)
	for _, message := range messages {
		if strings.Contains(message.Content, "ROOM_ALPHA_SHARED") {
			t.Error("A note reached B helper")
		}
	}
	requireTurnMemoryNotes(t, messages[0].Content, "ROOM_BETA_SHARED")
}

func gateCaptureMemoryWarnings(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	if actionErr := logger.EnableFileLogging(path); actionErr != nil {
		t.Fatal(actionErr)
	}
	t.Cleanup(logger.DisableFileLogging)
	level := logger.GetLevel()
	logger.SetLevel(logger.WARN)
	t.Cleanup(func() { logger.SetLevel(level) })
	logger.WarnCF("agent.memory", "warning capture control", map[string]any{"reason": "control"})
	return path
}

func gateHasWarning(t *testing.T, path, errorText string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	control, found := false, false
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var event map[string]any
		if actionErr := json.Unmarshal([]byte(line), &event); actionErr != nil {
			t.Fatal(actionErr)
		}
		if event["reason"] == "control" && event["level"] == "warn" {
			control = true
		}
		if detail, ok := event["error"].(string); ok && detail != "" && event["level"] == "warn" && strings.Contains(detail, errorText) {
			found = true
		}
	}
	if !control {
		t.Fatal("warning instrument missed its positive control")
	}
	return found
}

func gateMemoryScanError(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	// Keep an actual healthy room, then make the shared directory unusable.
	mf := memrooms.MemoryFile{Frontmatter: memrooms.MemoryFrontmatter{ID: "round2-private", Title: "roomnote", Type: memrooms.MemoryTypeReference, Status: memrooms.MemoryStatusActive, Author: "test-agent"}, Body: "roomnote private control"}
	if actionErr := memrooms.WriteMemoryFile(ms.PrivateRoom().MemoriesDir, mf); actionErr != nil {
		t.Fatal(actionErr)
	}
	shared := ms.SharedRoom(ms.scopeForWorkspace("beta"))
	if actionErr := os.MkdirAll(shared.Root, 0o700); actionErr != nil {
		t.Fatal(actionErr)
	}
	if actionErr := os.WriteFile(shared.MemoriesDir, []byte("not a directory"), 0o600); actionErr != nil {
		t.Fatal(actionErr)
	}
	if _, err := os.ReadDir(shared.MemoriesDir); err == nil {
		t.Fatal("scan-fault instrument did not fail")
	}
	entries, err := ms.SearchEntriesInScope("roomnote", 20, memrooms.RoomScopeBoth, ms.scopeForWorkspace("beta"))
	if err == nil || len(entries) != 0 {
		t.Errorf("failed room returned successful partial search: entries=%+v err=%v", entries, err)
	}
	tool := tools.NewRecallMemoryTool(NewMemoryStoreAdapter(ms))
	result := tool.Execute(tools.WithWorkspaceID(t.Context(), "beta"), map[string]any{"query": "roomnote"})
	if !result.IsError {
		t.Errorf("real recall hid room failure: %+v", result)
	}
	log := gateCaptureMemoryWarnings(t)
	text := ms.GetMemoryContextForScope(ms.scopeForWorkspace("beta"))
	if !strings.Contains(strings.ToLower(text), "error") {
		t.Errorf("prompt hid unavailable notes: %q", text)
	}
	if !gateHasWarning(t, log, "") {
		t.Error("prompt scan failure emitted no warning")
	}
}

func gateCitationIOError(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	if actionErr := ms.AppendLongTerm("roomnote citation control", "reference"); actionErr != nil {
		t.Fatal(actionErr)
	}
	entries, err := ms.SearchEntries("roomnote", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("citation input control: %+v %v", entries, err)
	}
	id := entries[0].ID
	tracker := newCitationTracker(ms, "alpha")
	tracker.RecordRecalled([]tools.MemoryEntry{{ID: id, Title: entries[0].Title}})
	log := gateCaptureMemoryWarnings(t)
	original := ms.privateRoom.MemoriesDir
	ms.privateRoom.MemoriesDir = original + "\x00"
	_, fault := os.Stat(filepath.Join(ms.privateRoom.MemoriesDir, memrooms.Filename(id)))
	if fault == nil || errors.Is(fault, os.ErrNotExist) {
		t.Fatalf("stat instrument must cause a non-not-found error: %v", fault)
	}
	tracker.EmitCitations("id:" + id)
	if tracker.emitted[id] {
		t.Error("stat failure was permanently marked emitted/deleted")
	}
	if !gateHasWarning(t, log, "") {
		t.Error("stat failure emitted no WARN")
	}
	ms.privateRoom.MemoriesDir = original
	tracker.EmitCitations("id:" + id)
	if got := countOp(readCounterRecords(t, ms.PrivateRoom().CountersPath), memrooms.CounterOpCited); got != 1 {
		t.Errorf("repaired citation did not retry: got %d want 1", got)
	}
	// A real missing file is not an I/O error; it can be retired once.
	missing := newCitationTracker(ms, "alpha")
	missing.RecordRecalled([]tools.MemoryEntry{{ID: "genuinely-missing"}})
	missing.EmitCitations("id:genuinely-missing")
	if !missing.emitted["genuinely-missing"] {
		t.Error("confirmed absence was not distinguished from I/O failure")
	}
}

func gateRenderScopeMismatch(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	log := gateCaptureMemoryWarnings(t)
	text := ms.GetMemoryContextForScope(MemoryScope{AgentID: "other", WorkspaceID: "beta"})
	if !strings.Contains(text, "scope agent does not match store") {
		t.Errorf("renderer hid identity error: %q", text)
	}
	if !gateHasWarning(t, log, "scope agent does not match store") {
		t.Error("scope mismatch emitted no diagnostic WARN")
	}
}
