package gateway

// SFH-01 / B07, office-preview §5.2–5.3 and the Retry ruling: a saved
// successful Retry must not revert to the previous unavailable group on a
// fresh snapshot. The review-follow-up brief explicitly includes the case
// where the original call lies outside the bounded latest history page.
// Real renderer, settlement, channel publication, hub and attach; no model.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func officeReviewOffpageAttach(t *testing.T, f *officeRetryFixture, chat string) []generated.MediaFrame {
	t.Helper()
	conn := &wsConn{sendCh: make(chan []byte, 256), doneCh: make(chan struct{})}
	f.ws.mu.Lock()
	f.ws.sessions[chat] = conn
	f.ws.mu.Unlock()
	f.ws.handleAttachSession(context.Background(), chat, f.sid, nil, conn)
	frames := pagingDrain(t, conn)
	require.Equal(t, 0, indexOfType(frames, "session_snapshot"), "instrument: real bounded snapshot, not incremental journal replay")
	complete := indexOfType(frames, "catch_up_complete")
	require.Greater(t, complete, indexOfType(frames, "done"))
	require.Equal(t, "snapshot", frames[complete].Mode)
	var selected []string
	var target []generated.MediaFrame
	for _, frame := range frames {
		switch frame.Type {
		case "replay_message":
			var replay generated.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(frame.raw, &replay))
			require.NotNil(t, replay.Id)
			selected = append(selected, *replay.Id)
		case "media":
			var media generated.MediaFrame
			require.NoError(t, json.Unmarshal(frame.raw, &media))
			if media.ToolCallId != nil && *media.ToolCallId == "retry-original-call" {
				target = append(target, media)
			}
		}
	}
	require.Equal(t, pagingWantIDs("offpage", 2, 41), selected, "instrument: original preview entry is genuinely outside the selected latest 40")
	return target
}

func TestOfficePreview_RetryOffpageSnapshotKeepsSavedGroup(t *testing.T) {
	f := newOfficeRetryFixture(t)
	officeRetrySharedRenderer(t)
	ctx := context.Background()
	stream, ok := f.ws.GetStreamer(ctx, "webchat", "retry-person", f.sid)
	require.True(t, ok)
	streamer, ok := stream.(*wsStreamer)
	require.True(t, ok)
	streamer.SetTurnID("offpage-unfinished-turn")
	streamer.SetMessageID("offpage-live-message")
	require.NoError(t, streamer.Update(ctx, "The original turn is still working."))
	require.NotEmpty(t, pagingDrain(t, f.conn), "instrument: unfinished turn really published, with no done")

	data := officepreview.PDF(1)
	require.NoError(t, os.WriteFile(filepath.Join(workDir(f.api, f.wsID), "report.pdf"), data, 0600))
	f.requireLibraryBytes(t, "report.pdf", data)
	f.record(t, "report.pdf", data)
	reason := "renderer_failed"
	original := &generated.MediaPreview{
		SourceName: "report.pdf", SourceVersion: officeRetryDigest(data),
		PageCount: 1, FirstPage: 1, LastPage: 1,
		Outcome: "unavailable", UnavailableReason: &reason,
	}
	require.NoError(t, newWebchatChannel(f.ws).SendMedia(ctx, bus.OutboundMediaMessage{
		ChatID: "retry-person", SessionID: f.sid, ToolCallID: "retry-original-call",
		Presentation: "page_thumbnails", Preview: original,
	}))
	initial := officeReceiveMedia(t, f.conn)
	require.Equal(t, original, initial.Preview)

	store := f.api.agentLoop.GetSessionStore()
	pagingSeed(t, store, f.sid, "offpage", 41)
	page := pagingRead(t, f.api, f.sid, "")
	require.True(t, page.HasEarlier)
	require.Equal(t, pagingWantIDs("offpage", 2, 41), pagingIDs(page))
	before := officeReviewOffpageAttach(t, f, "offpage-before-retry")
	require.Len(t, before, 1, "instrument: an off-page original group is actually present in the unfinished-turn projection")
	require.Equal(t, "unavailable", before[0].Preview.Outcome)
	require.Empty(t, before[0].Parts)

	response := f.retry(t, f.sid)
	require.Equal(t, http.StatusAccepted, response.Code, "%s", response.Body.String())
	live := officeReceiveMedia(t, f.conn)
	f.requireRenderedFrame(t, live)
	// Settlement precedes publication: independently check the durable call,
	// so a failed save can never masquerade as this successful-Retry case.
	entries, err := store.ReadTranscript(f.sid)
	require.NoError(t, err)
	var saved []generated.MediaFrame
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if call.ID == "retry-original-call" {
				frame, exists := buildMediaFrame(f.sid, call, f.api.mediaStore, nil, "", "")
				require.True(t, exists)
				saved = append(saved, frame)
			}
		}
	}
	require.Len(t, saved, 1)
	f.requireRenderedFrame(t, saved[0])
	require.Equal(t, officeMediaProjection(live), officeMediaProjection(saved[0]))
	t.Log("instrument: actual Retry rendered and saved one complete group before fresh bounded attach; original call excluded from latest 40")

	after := officeReviewOffpageAttach(t, f, "offpage-after-retry")
	for _, frame := range after {
		assert.NotEqual(t, "unavailable", frame.Preview.Outcome, "SFH-01: fresh attach must not resurrect the obsolete unavailable group")
	}
	require.Len(t, after, 1, "SFH-01 dispatch: the fresh snapshot must contain exactly the successful saved Retry group")
	assert.Equal(t, officeMediaProjection(live), officeMediaProjection(after[0]), "SFH-01: saved successful group replaces, rather than merely hiding, its previous state")
	require.Equal(t, int64(0), f.provider.calls.Load())
	require.Empty(t, f.bus.InboundChan(), "Retry/attach never starts a model turn")
}
