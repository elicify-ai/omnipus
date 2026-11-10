package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/memrooms"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Oracle: founder 2026-10-21, shared notes belong to the CURRENT chat's
// workspace; private notes belong to the agent. Exercise the real prompt,
// tools, adapter, index and session metadata, without calling a model.
func TestMemoryRoomPerTurn(t *testing.T) {
	t.Run("AThenB", testMemoryRoomAThenB)
	t.Run("ConcurrentSessions", testMemoryRoomConcurrentSessions)
	t.Run("ConcurrentCitations", testMemoryRoomConcurrentCitations)
	t.Run("NoWorkspace", testMemoryRoomNoWorkspace)
	t.Run("TeammateFreshness", testMemoryRoomTeammateFreshness)
}

type turnMemoryFixture struct {
	cb       *ContextBuilder
	home     string
	remember *tools.RememberTool
	recall   *tools.RecallMemoryTool
	sessions *session.UnifiedStore
}

func newTurnMemoryFixture(t *testing.T) turnMemoryFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", filepath.Join(home, "empty-skills"))
	cb := NewContextBuilder(filepath.Join(home, "agents", "self")).WithAgentInfo("self", "Self")
	t.Cleanup(cb.Memory().Close)
	store, err := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close session store: %v", err)
		}
	})
	adapter := NewMemoryStoreAdapter(cb.Memory())
	return turnMemoryFixture{cb, home, tools.NewRememberTool(adapter, nil), tools.NewRecallMemoryTool(adapter), store}
}

func (f turnMemoryFixture) chat(t *testing.T, workspaceID string) (*session.UnifiedMeta, context.Context) {
	t.Helper()
	meta, err := f.sessions.NewSession(session.SessionTypeChat, "webchat", "self")
	if err != nil {
		t.Fatal(err)
	}
	if setErr := f.sessions.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &workspaceID}); setErr != nil {
		t.Fatal(setErr)
	}
	meta, err = f.sessions.GetMeta(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), meta.ID), "self")
	return meta, tools.WithWorkspaceID(ctx, meta.WorkspaceID)
}

func (f turnMemoryFixture) save(t *testing.T, ctx context.Context, body, room string) {
	t.Helper()
	result := f.remember.Execute(ctx, map[string]any{"content": "roomnote\n" + body, "category": "reference", "room": room})
	if result == nil || result.IsError {
		t.Fatalf("remember %s in %s: %+v", body, room, result)
	}
}

func (f turnMemoryFixture) prompt(meta *session.UnifiedMeta) string {
	return f.cb.BuildMessages(0, nil, "show current chat notes", nil, meta.WorkspaceID,
		"webchat", meta.ID, "", "", "", nil)[0].Content
}

// The complete set of fixture-note body lines, not a substring presence check:
// any extra cross-workspace or cross-agent body makes the equality fail.
func requireTurnMemoryNotes(t *testing.T, text string, want ...string) {
	t.Helper()
	var got []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "ROOM_") {
			got = append(got, line)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chat memory bodies = %q, want exactly %q", got, want)
	}
}

func testMemoryRoomAThenB(t *testing.T) {
	f := newTurnMemoryFixture(t)
	a, ctxA := f.chat(t, "alpha")
	b, ctxB := f.chat(t, "beta")
	f.save(t, ctxB, "ROOM_BETA_SHARED", "shared")
	f.save(t, ctxA, "ROOM_ALPHA_SHARED", "shared")
	f.save(t, ctxA, "ROOM_SELF_PRIVATE", "private")

	// A is the last tool caller, exactly the reported precondition. B has
	// never used recall before its first prompt is assembled.
	requireTurnMemoryNotes(t, f.prompt(a), "ROOM_ALPHA_SHARED", "ROOM_SELF_PRIVATE")
	requireTurnMemoryNotes(t, f.prompt(b), "ROOM_BETA_SHARED", "ROOM_SELF_PRIVATE")
	result := f.recall.Execute(ctxB, map[string]any{"query": "roomnote"})
	if result == nil || result.IsError {
		t.Fatalf("B recall: %+v", result)
	}
	requireTurnMemoryNotes(t, result.ForLLM, "ROOM_BETA_SHARED", "ROOM_SELF_PRIVATE")

	other := NewContextBuilder(filepath.Join(f.home, "agents", "other")).WithAgentInfo("other", "Other")
	t.Cleanup(other.Memory().Close)
	requireTurnMemoryNotes(t, other.BuildSystemPromptForWorkspace("alpha"), "ROOM_ALPHA_SHARED")
}

func testMemoryRoomConcurrentSessions(t *testing.T) {
	f := newTurnMemoryFixture(t)
	a, ctxA := f.chat(t, "alpha")
	b, ctxB := f.chat(t, "beta")
	f.save(t, ctxA, "ROOM_SELF_PRIVATE", "private")
	aSaved, bSaved := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		func() {
			defer close(aSaved) // release B even if fixture setup fails
			f.save(t, ctxA, "ROOM_ALPHA_SHARED", "shared")
		}()
		<-bSaved
		// Deterministic interleaving: B saved while A's session is still
		// alive. A must not inherit B's room when it builds its prompt.
		requireTurnMemoryNotes(t, f.prompt(a), "ROOM_ALPHA_SHARED", "ROOM_SELF_PRIVATE")
	}()
	go func() {
		defer wg.Done()
		<-aSaved
		func() {
			defer close(bSaved) // release A even if fixture setup fails
			f.save(t, ctxB, "ROOM_BETA_SHARED", "shared")
		}()
		requireTurnMemoryNotes(t, f.prompt(b), "ROOM_BETA_SHARED", "ROOM_SELF_PRIVATE")
	}()
	wg.Wait()

	// Both real tool instances are shared by the two sessions. Exercise
	// reads/writes together, checking disk routing as well as prompt output.
	for _, chat := range []struct {
		meta *session.UnifiedMeta
		ctx  context.Context
		own  string
	}{
		{a, ctxA, "ALPHA"},
		{b, ctxB, "BETA"},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 8 { // contention fixture count, not a product limit
				body := fmt.Sprintf("ROOM_%s_WRITE_%d", chat.own, i)
				f.save(t, chat.ctx, body, "shared")
				result := f.recall.Execute(chat.ctx, map[string]any{"query": body, "room": "shared"})
				if result == nil || result.IsError || !strings.Contains(result.ForLLM, body) {
					t.Errorf("session %s lost its own recall %s: %+v", chat.meta.ID, body, result)
				}
			}
		}()
	}
	wg.Wait()
	for _, ws := range []string{"alpha", "beta"} {
		room := memrooms.ResolveWorkspaceSharedRoom(f.home, ws)
		notes, err := memrooms.ScanMemories(room.MemoriesDir)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, note := range notes {
			got = append(got, note.Body)
		}
		want := make([]string, 0, 9) // one shared seed plus eight concurrent writes
		want = append(want, "ROOM_"+strings.ToUpper(ws)+"_SHARED")
		for i := range 8 {
			want = append(want, fmt.Sprintf("ROOM_%s_WRITE_%d", strings.ToUpper(ws), i))
		}
		requireTurnMemoryNotes(t, strings.Join(got, "\n"), want...)
		sharedCtx := ctxA
		if ws == "beta" {
			sharedCtx = ctxB
		}
		// A common term exposes over-inclusion: own-note presence alone
		// would accept foreign or private notes returned alongside it.
		result := f.recall.Execute(sharedCtx, map[string]any{"query": "roomnote", "room": "shared"})
		if result == nil || result.IsError {
			t.Fatalf("%s shared-only recall: %+v", ws, result)
		}
		requireTurnMemoryNotes(t, result.ForLLM, want...)
	}
}

func testMemoryRoomNoWorkspace(t *testing.T) {
	f := newTurnMemoryFixture(t)
	a, ctxA := f.chat(t, "alpha")
	none, ctxNone := f.chat(t, "")
	f.save(t, ctxA, "ROOM_ALPHA_SHARED", "shared")
	f.save(t, ctxA, "ROOM_SELF_PRIVATE", "private")
	requireTurnMemoryNotes(t, f.prompt(a), "ROOM_ALPHA_SHARED", "ROOM_SELF_PRIVATE")

	logPath := filepath.Join(t.TempDir(), "memory.jsonl")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(logger.DisableFileLogging)
	oldLevel := logger.GetLevel()
	logger.SetLevel(logger.WARN)
	t.Cleanup(func() { logger.SetLevel(oldLevel) })
	logger.WarnCF("agent.memory", "memory warning positive control", map[string]any{"reason": "control"})

	requireTurnMemoryNotes(t, f.prompt(none), "ROOM_SELF_PRIVATE")
	// Invalid identifiers must fail closed before path resolution too.
	requireTurnMemoryNotes(t, f.cb.BuildSystemPromptForWorkspace("../alpha"), "ROOM_SELF_PRIVATE")
	result := f.recall.Execute(ctxNone, map[string]any{"query": "roomnote"})
	if result == nil || result.IsError {
		t.Fatalf("no-workspace recall: %+v", result)
	}
	requireTurnMemoryNotes(t, result.ForLLM, "ROOM_SELF_PRIVATE")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	foundControl, foundMissing := false, false
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode captured warning: %v", err)
		}
		if event["reason"] == "control" && event["level"] == "warn" {
			foundControl = true
		}
		if event["reason"] == "no_workspace" && event["level"] == "warn" && event["workspace_id"] == "" {
			foundMissing = true
		}
	}
	if !foundControl {
		t.Fatal("warning capture instrument missed its positive control")
	}
	if !foundMissing {
		t.Error("missing workspace must emit WARN with reason=no_workspace and workspace_id empty")
	}

	// Keep the documented explicit shared-write fallback, but it must not
	// affect A's shared room or misreport where the new note was stored.
	result = f.remember.Execute(ctxNone, map[string]any{"content": "ROOM_NO_WORKSPACE_PRIVATE", "category": "reference", "room": "shared"})
	if result == nil || result.IsError || result.ForLLM != "ok — saved as a PRIVATE note: this turn has no workspace, so there is no team room to share into" {
		t.Fatalf("shared without workspace must report private fallback: %+v", result)
	}
	requireTurnMemoryNotes(t, f.prompt(none), "ROOM_SELF_PRIVATE", "ROOM_NO_WORKSPACE_PRIVATE")
}

func testMemoryRoomTeammateFreshness(t *testing.T) {
	for _, seedShared := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_room_%t", seedShared), func(t *testing.T) {
			f := newTurnMemoryFixture(t)
			a, ctxA := f.chat(t, "alpha")
			f.save(t, ctxA, "ROOM_SELF_PRIVATE", "private")
			want := []string{"ROOM_SELF_PRIVATE"}
			if seedShared {
				f.save(t, ctxA, "ROOM_ALPHA_INITIAL", "shared")
				want = append(want, "ROOM_ALPHA_INITIAL")
			}
			requireTurnMemoryNotes(t, f.prompt(a), want...)
			first := f.cb.workspacePromptCache.variants["alpha"]
			if first == nil {
				t.Fatal("cache freshness positive control: initial prompt was not cached")
			}
			requireTurnMemoryNotes(t, f.prompt(a), want...)
			if f.cb.workspacePromptCache.variants["alpha"] != first {
				t.Fatal("cache freshness positive control: unchanged prompt did not hit cache")
			}

			peer := NewMemoryStore(filepath.Join(f.home, "agents", "peer"), f.home)
			t.Cleanup(peer.Close)
			peerTool := tools.NewRememberTool(NewMemoryStoreAdapter(peer), nil)
			result := peerTool.Execute(ctxA, map[string]any{"content": "roomnote\nROOM_ALPHA_TEAMMATE_NEW", "category": "reference", "room": "shared"})
			if result == nil || result.IsError {
				t.Fatalf("teammate remember: %+v", result)
			}
			want = append(want, "ROOM_ALPHA_TEAMMATE_NEW")
			requireTurnMemoryNotes(t, f.prompt(a), want...)
		})
	}
}

// Both citation trackers belong to concurrent sessions of one agent. B's
// recall occurs between A's recall and A's citation, so citation routing must
// retain A's original workspace rather than following another session.
func testMemoryRoomConcurrentCitations(t *testing.T) {
	f := newTurnMemoryFixture(t)
	now := time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)
	f.cb.Memory().SetClock(func() time.Time { return now })
	_, ctxA := f.chat(t, "alpha")
	_, ctxB := f.chat(t, "beta")
	f.save(t, ctxA, "ROOM_ALPHA_SHARED", "shared")
	f.save(t, ctxB, "ROOM_BETA_SHARED", "shared")
	f.save(t, ctxA, "ROOM_SELF_PRIVATE", "private")
	trackers := map[string]*citationTracker{
		"alpha": newCitationTracker(f.cb.Memory(), "alpha"),
		"beta":  newCitationTracker(f.cb.Memory(), "beta"),
	}
	ids := make(map[string]string, 2)
	for _, chat := range []struct {
		ws  string
		ctx context.Context
	}{
		{"alpha", ctxA},
		{"beta", ctxB},
	} {
		room := memrooms.ResolveWorkspaceSharedRoom(f.home, chat.ws)
		files, err := memrooms.ScanMemories(room.MemoriesDir)
		if err != nil || len(files) != 1 {
			t.Fatalf("%s citation fixture must have one persisted shared note: files=%+v err=%v", chat.ws, files, err)
		}
		ids[chat.ws] = files[0].Frontmatter.ID
		if ids[chat.ws] == "" {
			t.Fatalf("%s citation fixture has no stable ID", chat.ws)
		}
		ctx := tools.WithCitationTracker(chat.ctx, trackers[chat.ws])
		result := f.recall.Execute(ctx, map[string]any{"query": "roomnote", "room": "shared"})
		if result == nil || result.IsError {
			t.Fatalf("%s tracked shared recall: %+v", chat.ws, result)
		}
		requireTurnMemoryNotes(t, result.ForLLM, "ROOM_"+strings.ToUpper(chat.ws)+"_SHARED")
	}

	// The expected IDs come from the input files, independently of recall's
	// output. The fixed clock makes the ENTIRE cited counter record exact.
	for _, ws := range []string{"alpha", "beta"} {
		trackers[ws].EmitCitations("Based on memory id:" + ids[ws])
	}
	for _, ws := range []string{"alpha", "beta"} {
		room := memrooms.ResolveWorkspaceSharedRoom(f.home, ws)
		cited := make([]memrooms.CounterRecord, 0, 1) // exactly one shared note in this fixture
		for _, record := range readCounterRecords(t, room.CountersPath) {
			if record.Op == memrooms.CounterOpCited {
				cited = append(cited, record)
			}
		}
		want := []memrooms.CounterRecord{{TS: now, MemoryID: ids[ws], Op: memrooms.CounterOpCited, By: "self"}}
		if !reflect.DeepEqual(cited, want) {
			t.Errorf("%s cited records = %+v, want exactly %+v", ws, cited, want)
		}
	}
	private := readCounterRecords(t, f.cb.Memory().PrivateRoom().CountersPath)
	if got := countOp(private, memrooms.CounterOpCited); got != 0 {
		t.Errorf("shared citations added %d records to private memory, want zero", got)
	}
}
