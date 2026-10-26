package gateway

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func historyCrossGroupPNG(t *testing.T, path string) {
	t.Helper()
	file, createErr := os.Create(path)
	require.NoError(t, createErr)
	picture := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	picture.SetNRGBA(0, 0, color.NRGBA{R: 17, G: 31, B: 47, A: 255})
	encodeErr := png.Encode(file, picture)
	closeErr := file.Close()
	require.NoError(t, encodeErr)
	require.NoError(t, closeErr)
}

func historyCrossGroupEntry(t *testing.T, store *session.UnifiedStore, sid, id, callID, ref, filename string) {
	t.Helper()
	require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: id, Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia",
		Timestamp: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{{ID: session.ToolCallID(callID), Tool: "send_file", Status: "success", Parameters: map[string]any{}, Result: map[string]any{"media": []any{map[string]any{"ref": ref, "filename": filename, "content_type": "image/png", "type": "image"}}}}},
	}))
}

func historyCrossGroupSnapshot(t *testing.T, fixture *attachFixture, viewer string) ([]generated.MediaPart, []generated.HistoryEntryOrigin) {
	t.Helper()
	connection := fixture.newConn(viewer)
	fixture.h.handleAttachSession(context.Background(), viewer, fixture.sid, nil, connection)
	frames := pagingDrain(t, connection)
	require.GreaterOrEqual(t, indexOfType(frames, "session_snapshot"), 0, "actual prepared snapshot, not an incremental shortcut")
	complete := indexOfType(frames, "catch_up_complete")
	require.GreaterOrEqual(t, complete, 0)
	require.Equal(t, "snapshot", frames[complete].Mode)
	var parts []generated.MediaPart
	var origins []generated.HistoryEntryOrigin
	for _, frame := range frames {
		if frame.Type != "media" {
			continue
		}
		var wire generated.MediaFrame
		require.NoError(t, json.Unmarshal(frame.raw, &wire))
		require.Equal(t, fixture.sid, wire.SessionId)
		require.NotNil(t, wire.HistoryOrigin, "persisted media keeps its actual owning source group")
		for _, part := range wire.Parts {
			parts = append(parts, part)
			origins = append(origins, *wire.HistoryOrigin)
		}
	}
	return parts, origins
}

// D4 parity: two distinct legacy refs for one physical image must be suppressed
// across source groups, not only within a group's temporary replay state.
func TestHistoryPage_CrossGroupMediaDeduplicatesPhysicalPathLikeWholeReplay(t *testing.T) {
	fixture := newAttachFixture(t, "cross-group-media-turn", "cross-group-media-message")
	mediaStore := newTestFileMediaStore(t)
	fixture.h.agentLoop.SetMediaStore(mediaStore)
	sharedPath := filepath.Join(t.TempDir(), "shared.png")
	otherPath := filepath.Join(t.TempDir(), "other.png")
	historyCrossGroupPNG(t, sharedPath)
	historyCrossGroupPNG(t, otherPath)
	metadata := media.MediaMeta{Filename: "shared.png", ContentType: "image/png", CleanupPolicy: media.CleanupPolicyForgetOnly}
	firstRef, firstErr := mediaStore.Store(sharedPath, metadata, media.SessionInlineScopePrefix+fixture.sid)
	require.NoError(t, firstErr)
	secondRef, secondErr := mediaStore.Store(sharedPath, metadata, media.SessionInlineScopePrefix+fixture.sid)
	require.NoError(t, secondErr)
	otherRef, otherErr := mediaStore.Store(otherPath, media.MediaMeta{Filename: "other.png", ContentType: "image/png", CleanupPolicy: media.CleanupPolicyForgetOnly}, media.SessionInlineScopePrefix+fixture.sid)
	require.NoError(t, otherErr)
	require.NotEqual(t, firstRef, secondRef, "two REAL distinct registry refs, not a repeated ref-string fixture")
	for _, ref := range []string{firstRef, secondRef} {
		resolved, resolveErr := mediaStore.ResolveWithOpts(ref, media.ResolveOpts{})
		require.NoError(t, resolveErr)
		require.Equal(t, sharedPath, resolved)
	}
	resolvedOther, resolveErr := mediaStore.Resolve(otherRef)
	require.NoError(t, resolveErr)
	require.Equal(t, otherPath, resolvedOther)
	require.NotEqual(t, sharedPath, resolvedOther, "instrument must preserve a genuinely different physical image")
	historyCrossGroupEntry(t, fixture.store, fixture.sid, "media-group-first", "media-call-first", firstRef, "shared.png")
	one, _ := historyCrossGroupSnapshot(t, fixture, "media-single-group")
	require.Len(t, one, 1, "mandatory actual single-group snapshot control; no universal media suppression")
	require.Equal(t, "image", one[0].Type)
	require.Equal(t, "shared.png", one[0].Filename)
	require.Equal(t, "image/png", one[0].ContentType)
	historyCrossGroupEntry(t, fixture.store, fixture.sid, "media-group-second", "media-call-second", secondRef, "shared.png")
	historyCrossGroupEntry(t, fixture.store, fixture.sid, "media-group-other", "media-call-other", otherRef, "other.png")
	entries, readErr := fixture.store.ReadTranscript(fixture.sid)
	require.NoError(t, readErr)
	require.Len(t, entries, 3)
	var whole []generated.MediaPart
	_, replayErr := streamReplay(context.Background(), fixture.sid, entries, computeReplayStats(entries), func(frame any) error {
		if wire, ok := frame.(generated.MediaFrame); ok {
			whole = append(whole, wire.Parts...)
		}
		return nil
	}, mediaStore, nil, nil, nil)
	require.NoError(t, replayErr)
	require.Len(t, whole, 2, "whole-batch replay positive control: one shared path plus one distinct path")
	require.Equal(t, one[0], whole[0])
	require.Equal(t, "other.png", whole[1].Filename)
	require.NotEqual(t, whole[0].Url, whole[1].Url)
	t.Log("CROSS_GROUP_MEDIA_CONTROLS=PASS real aliases, single-group snapshot, distinct path and whole-batch replay")
	actual, origins := historyCrossGroupSnapshot(t, fixture, "media-cross-group")
	require.Equal(t, whole, actual, "D4 cross-group carrier RED: actual snapshot must emit the same two images, not the aliased image twice")
	require.Len(t, origins, 2)
	for index, id := range []string{"media-group-first", "media-group-other"} {
		require.NotNil(t, origins[index].EntryId)
		require.Equal(t, id, *origins[index].EntryId)
		require.Regexp(t, `^hp1_[A-Za-z0-9_-]{43}$`, origins[index].Position)
	}
}
