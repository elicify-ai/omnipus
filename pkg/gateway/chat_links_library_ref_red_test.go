// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED: MediaPart.library_ref (FR-021, BS-4.4, BS-5.1, BS-5.4).
//
// A file inside the workspace work directory is workspace kind, relative path
// notes/plan.md (BS-4.4 first row). The same fill happens on live SendMedia
// (the outbound message's workspace) and on history-page preparation (the
// session meta workspace, once per page). A file outside every Library, a
// session with no workspace, and a path that is the Library root itself omit
// the field. The root is not a valid target: the frame never carries path "".
// This file does not call libraryRefFor; that function does not exist yet.
package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

const (
	chatLinksWorkspaceID = "W"
	chatLinksPlanRel     = "notes/plan.md"
)

func chatLinksHome(t *testing.T, h *WSHandler) string {
	t.Helper()
	home := h.agentLoop.GetConfig().Agents.Defaults.Home
	require.NotEmpty(t, home, "instrument: agent home")
	h.home = home
	return home
}

func chatLinksStore(t *testing.T, store *media.FileMediaStore, path, filename, contentType, scope string) string {
	t.Helper()
	ref, err := store.Store(path, media.MediaMeta{
		Filename: filename, ContentType: contentType, CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, scope)
	require.NoError(t, err)
	resolved, resolveErr := store.Resolve(ref)
	require.NoError(t, resolveErr)
	require.Equal(t, path, resolved, "instrument: the ref must resolve to the placed file")
	return ref
}

func chatLinksSendFileEntry(t *testing.T, store *session.UnifiedStore, sid, id, callID, ref, filename, contentType, mediaType string) {
	t.Helper()
	require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: id, Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia",
		Timestamp: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{{
			ID: session.ToolCallID(callID), Tool: "send_file", Status: "success",
			Parameters: map[string]any{},
			Result: map[string]any{"media": []any{map[string]any{
				"ref": ref, "filename": filename, "content_type": contentType, "type": mediaType,
			}}},
		}},
	}))
}

func chatLinksTakeMedia(t *testing.T, conn *wsConn) (generated.MediaFrame, []byte) {
	t.Helper()
	select {
	case raw := <-conn.sendCh:
		var frame generated.MediaFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		require.Equal(t, "media", frame.Type, "instrument: %s", raw)
		require.NotEmpty(t, frame.Parts, "instrument: media frame has no parts")
		return frame, raw
	case <-time.After(2 * time.Second):
		t.Fatal("instrument: bound connection got no media frame (FR-021)")
	}
	return generated.MediaFrame{}, nil
}

func chatLinksSendLive(t *testing.T, h *WSHandler, sessionID, workspaceID, ref, filename, contentType, mediaType string) (generated.MediaFrame, []byte) {
	t.Helper()
	conn := makeTestConn()
	bindTestConnToSession(h, "chat-"+sessionID, sessionID, conn)
	err := newWebchatChannel(h).SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "chat-" + sessionID, SessionID: sessionID, WorkspaceID: workspaceID,
		Parts: []bus.MediaPart{{Ref: ref, Filename: filename, ContentType: contentType, Type: mediaType}},
	})
	require.NoError(t, err)
	return chatLinksTakeMedia(t, conn)
}

func chatLinksRequireRef(t *testing.T, part generated.MediaPart, raw []byte) {
	t.Helper()
	if bytesContains(raw, `"path":""`) {
		t.Fatalf("empty library path emitted (FR-021): %s", raw)
	}
	if part.LibraryRef == nil {
		t.Errorf("media part %q has no library_ref, want workspace %s path %s (FR-021 / BS-4.4)", part.Filename, chatLinksWorkspaceID, chatLinksPlanRel)
		return
	}
	if part.LibraryRef.WorkspaceId != chatLinksWorkspaceID || part.LibraryRef.Path != chatLinksPlanRel {
		t.Errorf("library_ref = {workspace %q path %q}, want {%q %q} (FR-021 / BS-4.4)",
			part.LibraryRef.WorkspaceId, part.LibraryRef.Path, chatLinksWorkspaceID, chatLinksPlanRel)
	}
}

func chatLinksRequireOmitted(t *testing.T, part generated.MediaPart, raw []byte, why string) {
	t.Helper()
	if bytesContains(raw, `"path":""`) {
		t.Fatalf("empty library path emitted for %s (FR-021): %s", why, raw)
	}
	if part.LibraryRef != nil || bytesContains(raw, `"library_ref"`) {
		t.Fatalf("library_ref = %+v, want omitted for %s (FR-021)", part.LibraryRef, why)
	}
}

func chatLinksSnapshotByFilename(t *testing.T, fixture *attachFixture, sid, viewer string) (map[string]generated.MediaPart, map[string][]byte) {
	t.Helper()
	conn := fixture.newConn(viewer)
	fixture.h.handleAttachSession(context.Background(), viewer, sid, nil, conn)
	frames := pagingDrain(t, conn)
	require.GreaterOrEqual(t, indexOfType(frames, "catch_up_complete"), 0, "instrument: snapshot did not finish")
	parts := map[string]generated.MediaPart{}
	raws := map[string][]byte{}
	for _, frame := range frames {
		if frame.Type != "media" {
			continue
		}
		var wire generated.MediaFrame
		require.NoError(t, json.Unmarshal(frame.raw, &wire))
		for _, part := range wire.Parts {
			parts[part.Filename] = part
			raws[part.Filename] = frame.raw
		}
	}
	return parts, raws
}

func TestLibraryRef_WorkspaceFileOnLiveSendAndHistory(t *testing.T) {
	handler, _, loop := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	home := chatLinksHome(t, handler)
	work, err := workspace.SafeWorkDir(home, chatLinksWorkspaceID)
	require.NoError(t, err)
	plan := filepath.Join(work, "notes", "plan.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(plan), 0o755))
	require.NoError(t, os.WriteFile(plan, []byte("plan"), 0o644))
	outside := filepath.Join(home, "outside-plan.md")
	require.NoError(t, os.WriteFile(outside, []byte("out"), 0o644))

	store := newTestFileMediaStore(t)
	loop.SetMediaStore(store)
	scope := media.SessionInlineScopePrefix + "chat-links"
	planRef := chatLinksStore(t, store, plan, "plan.md", "text/markdown", scope)
	outsideRef := chatLinksStore(t, store, outside, "outside.md", "text/markdown", scope)
	rootRef := chatLinksStore(t, store, work, "library-root", "application/octet-stream", scope)

	live, liveRaw := chatLinksSendLive(t, handler, "live-plan", chatLinksWorkspaceID, planRef, "plan.md", "text/markdown", "file")
	chatLinksRequireRef(t, live.Parts[0], liveRaw)

	fixture := newAttachFixture(t, "libref-turn", "libref-message")
	chatLinksHome(t, fixture.h)
	fixture.h.agentLoop.SetMediaStore(store)
	ws := chatLinksWorkspaceID
	require.NoError(t, fixture.store.SetMeta(fixture.sid, session.MetaPatch{WorkspaceID: &ws}))
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "libref-plan", "call-plan", planRef, "plan.md", "text/markdown", "file")
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "libref-out", "call-out", outsideRef, "outside.md", "text/markdown", "file")
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "libref-root", "call-root", rootRef, "library-root", "application/octet-stream", "file")
	parts, raws := chatLinksSnapshotByFilename(t, fixture, fixture.sid, "libref-viewer")
	planPart, ok := parts["plan.md"]
	if !ok {
		t.Fatal("instrument: history snapshot has no plan.md media part (FR-021 / BS-5.4)")
	}
	chatLinksRequireRef(t, planPart, raws["plan.md"])
	if t.Failed() {
		return
	}

	outPart, ok := parts["outside.md"]
	if !ok {
		t.Fatal("instrument: history snapshot has no outside.md media part")
	}
	chatLinksRequireOmitted(t, outPart, raws["outside.md"], "a file outside every Library")
	rootPart, ok := parts["library-root"]
	if !ok {
		t.Fatal("instrument: history snapshot has no Library-root media part")
	}
	chatLinksRequireOmitted(t, rootPart, raws["library-root"], "the Library root")

	bare := newAttachFixture(t, "libref-bare-turn", "libref-bare-message")
	chatLinksHome(t, bare.h)
	bare.h.agentLoop.SetMediaStore(store)
	chatLinksSendFileEntry(t, bare.store, bare.sid, "libref-bare", "call-bare", planRef, "plan.md", "text/markdown", "file")
	bareParts, bareRaws := chatLinksSnapshotByFilename(t, bare, bare.sid, "libref-bare-viewer")
	barePlan, ok := bareParts["plan.md"]
	if !ok {
		t.Fatal("instrument: no-workspace history snapshot has no plan.md media part")
	}
	chatLinksRequireOmitted(t, barePlan, bareRaws["plan.md"], "a session with no workspace")

	outLive, outRaw := chatLinksSendLive(t, handler, "live-out", chatLinksWorkspaceID, outsideRef, "outside.md", "text/markdown", "file")
	chatLinksRequireOmitted(t, outLive.Parts[0], outRaw, "a file outside every Library")
	rootLive, rootRaw := chatLinksSendLive(t, handler, "live-root", chatLinksWorkspaceID, rootRef, "library-root", "application/octet-stream", "file")
	chatLinksRequireOmitted(t, rootLive.Parts[0], rootRaw, "the Library root")
	noneLive, noneRaw := chatLinksSendLive(t, handler, "live-none", "", planRef, "plan.md", "text/markdown", "file")
	chatLinksRequireOmitted(t, noneLive.Parts[0], noneRaw, "a send with no workspace")
}
