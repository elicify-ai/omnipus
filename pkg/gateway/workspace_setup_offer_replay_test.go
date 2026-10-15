package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Offer-line text is the producer contract (workspace_setup_offer.go): the
// chat line is "Workspace setup offered for <name>." and, when the turn
// cannot be published, "The setup offer for <name> could not be started.
// Open its Team tab to set up its team." The live frame must be the same
// replay_message streamReplay emits for that saved entry (role system, entry
// id, content). The SPA drops a second delivery of that id
// (src/store/chat/slices/replay-and-status-frames.ts::replayAlreadyApplied).

const (
	setupOfferReplayName    = "Northwind"
	setupOfferReplayContent = "Workspace setup offered for Northwind."
	setupOfferReplayNote    = "The setup offer for Northwind could not be started. Open its Team tab to set up its team."
	setupOfferReplayUnwired = "workspace setup offer: saved the line but could not publish it; no session hub is wired"
)

func TestWorkspaceSetupOffer_AttachedSessionReceivesReplayMessageAndJournal(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	reg := newHubRegistry("boot-setup-offer")
	h.api.sessionHubs = reg
	live := newFakeHubConn("attached")
	hub := reg.getOrCreate(h.mainID)
	head, ok := hub.bindLive(live)
	require.True(t, ok, "the already-attached chat must be bound before the offer is posted")

	err := restAPISetupOfferPoster{a: h.api}.PostWorkspaceSetupOffer(
		context.Background(), "ws-northwind", setupOfferReplayName)
	require.NoError(t, err)

	offer := requireSetupOfferEntry(t, h, setupOfferReplayContent)
	require.Equal(t, "system", offer.Role, "the offer line is a system entry")
	require.Equal(t, session.ViewMembershipChat, offer.ViewMembership,
		"the offer line belongs to the chat view, so snapshot replay includes it")
	want := replayFrameForEntry(t, h.mainID, offer)

	got := decodeReplayMessages(t, live.snapshot())
	require.Equal(t, []generated.ReplayMessageFrame{want}, got,
		"an already-attached session must receive one replay_message for the saved offer entry")

	require.Len(t, hub.journal, 1, "the offer frame must be journaled exactly once")
	journaled := decodeReplayMessages(t, [][]byte{hub.journal[0].bytes})
	require.Equal(t, []generated.ReplayMessageFrame{want}, journaled,
		"the journaled frame is the same replay_message the attached session received")

	seq, hasSeq := decodeSeq(t, hub.journal[0].bytes)
	require.True(t, hasSeq, "a journaled frame carries a seq so a cursor can name it")

	late := newFakeHubConn("incremental")
	inc := hub.bind(late, i64p(int64(head)), hubStrp(reg.bootID), reg.bootID)
	require.True(t, inc.Servable, "a cursor from before the offer is inside the journal")
	require.Equal(t, []generated.ReplayMessageFrame{want}, decodeReplayMessages(t, inc.Tail),
		"incremental re-attach delivers the journaled offer frame once")

	caughtUp := newFakeHubConn("caught-up")
	done := hub.bind(caughtUp, i64p(seq), hubStrp(reg.bootID), reg.bootID)
	require.True(t, done.Servable)
	require.Empty(t, decodeReplayMessages(t, done.Tail),
		"a cursor on the offer frame itself must not deliver that frame again")

	snapshot := replayFramesOf(t, h.mainID, readMainTranscript(t, h))
	require.Equal(t, []generated.ReplayMessageFrame{want}, framesWithID(snapshot, offer.ID),
		"snapshot replay emits the offer entry once, with the same id as the live frame")

	cold := newFakeHubConn("snapshot")
	snapBind := hub.bind(cold, nil, nil, reg.bootID)
	require.False(t, snapBind.Servable, "a cold attach takes the snapshot path, not the journal tail")
	require.Empty(t, projectionFrames(h.mainID, snapBind.Proj, map[string]bool{}),
		"the offer frame is not also in the active-turn projection, so snapshot replay does not emit a second copy")
}

func TestWorkspaceSetupOffer_FailureNotePublishedToAttachedSession(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	reg := newHubRegistry("boot-setup-offer-note")
	h.api.sessionHubs = reg
	live := newFakeHubConn("attached")
	hub := reg.getOrCreate(h.mainID)
	head, ok := hub.bindLive(live)
	require.True(t, ok)
	h.bus.Close()

	err := restAPISetupOfferPoster{a: h.api}.PostWorkspaceSetupOffer(
		context.Background(), "ws-northwind", setupOfferReplayName)
	require.ErrorContains(t, err, "publish the setup offer")

	offer := requireSetupOfferEntry(t, h, setupOfferReplayContent)
	note := requireSetupOfferEntry(t, h, setupOfferReplayNote)
	require.NotEqual(t, offer.ID, note.ID, "the failure note is its own transcript entry")
	wantOffer := replayFrameForEntry(t, h.mainID, offer)
	wantNote := replayFrameForEntry(t, h.mainID, note)

	require.Equal(t, []generated.ReplayMessageFrame{wantOffer, wantNote}, decodeReplayMessages(t, live.snapshot()),
		"the attached session receives the offer line and then the failure note")
	require.Len(t, hub.journal, 2, "both saved lines are journaled, each once")

	late := newFakeHubConn("incremental")
	inc := hub.bind(late, i64p(int64(head)), hubStrp(reg.bootID), reg.bootID)
	require.True(t, inc.Servable)
	require.Equal(t, []generated.ReplayMessageFrame{wantOffer, wantNote}, decodeReplayMessages(t, inc.Tail),
		"incremental re-attach delivers both journaled lines once, in order")

	snapshot := replayFramesOf(t, h.mainID, readMainTranscript(t, h))
	require.Equal(t, []generated.ReplayMessageFrame{wantOffer}, framesWithID(snapshot, offer.ID))
	require.Equal(t, []generated.ReplayMessageFrame{wantNote}, framesWithID(snapshot, note.ID),
		"snapshot replay emits each saved line once")
}

func TestWorkspaceSetupOffer_UnwiredHubKeepsSavedLineAndLogs(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	err := restAPISetupOfferPoster{a: h.api}.PostWorkspaceSetupOffer(
		context.Background(), "ws-northwind", setupOfferReplayName)
	require.NoError(t, err, "a missed live publish must not fail the offer or undo the saved line")

	offer := requireSetupOfferEntry(t, h, setupOfferReplayContent)
	require.Equal(t, 1, strings.Count(logs.String(), setupOfferReplayUnwired),
		"the missed publish is logged once, not swallowed")
	require.Contains(t, logs.String(), offer.ID, "the log names the saved entry")
}

func requireSetupOfferEntry(t *testing.T, h *l1WU10SetupHarness, content string) session.TranscriptEntry {
	t.Helper()
	var found []session.TranscriptEntry
	for _, entry := range readMainTranscript(t, h) {
		if entry.Content == content {
			found = append(found, entry)
		}
	}
	require.Len(t, found, 1, "transcript must contain exactly one %q line", content)
	require.NotEmpty(t, found[0].ID)
	return found[0]
}

func readMainTranscript(t *testing.T, h *l1WU10SetupHarness) []session.TranscriptEntry {
	t.Helper()
	entries, err := h.store.ReadTranscript(h.mainID)
	require.NoError(t, err)
	return entries
}

func replayFrameForEntry(t *testing.T, sessionID string, entry session.TranscriptEntry) generated.ReplayMessageFrame {
	t.Helper()
	frames := replayFramesOf(t, sessionID, []session.TranscriptEntry{entry})
	require.Len(t, frames, 1, "streamReplay emits one replay_message for a non-empty system entry")
	require.Equal(t, "replay_message", frames[0].Type)
	require.Equal(t, "system", frames[0].Role)
	require.Equal(t, entry.Content, frames[0].Content)
	require.NotNil(t, frames[0].Id)
	require.Equal(t, entry.ID, *frames[0].Id)
	require.Equal(t, sessionID, frames[0].SessionId)
	return frames[0]
}

func replayFramesOf(t *testing.T, sessionID string, entries []session.TranscriptEntry) []generated.ReplayMessageFrame {
	t.Helper()
	sink := &sliceSink{}
	_, err := streamReplay(context.Background(), sessionID, entries, computeReplayStats(entries), sink.emit, nil, nil, nil, nil)
	require.NoError(t, err)
	sink.mu.Lock()
	raws := append([][]byte(nil), sink.frames...)
	sink.mu.Unlock()
	return decodeReplayMessages(t, raws)
}

func framesWithID(frames []generated.ReplayMessageFrame, id string) []generated.ReplayMessageFrame {
	var out []generated.ReplayMessageFrame
	for _, frame := range frames {
		if frame.Id != nil && *frame.Id == id {
			out = append(out, frame)
		}
	}
	return out
}

func decodeReplayMessages(t *testing.T, raws [][]byte) []generated.ReplayMessageFrame {
	t.Helper()
	out := make([]generated.ReplayMessageFrame, 0)
	for _, raw := range raws {
		var probe struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(raw, &probe), "frame must be JSON: %s", raw)
		if probe.Type != string(generated.WsFrameTypeReplayMessage) {
			continue
		}
		var frame generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		out = append(out, frame)
	}
	return out
}
