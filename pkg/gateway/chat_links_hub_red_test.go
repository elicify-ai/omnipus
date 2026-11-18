// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for live open_in_library delivery (FR-016, BS-6.1, BS-6.7, BS-6.11,
// BS-6.12, SEC-8, tests 14 and 15).
//
// The frame is observed on bound connections after hubToolExecEnd. This file
// does not name deliverLive: that method does not exist yet, and a direct
// call would stop pkg/gateway compiling. When ToolExecEndPayload grows
// OpenInLibrary, the helper below fills it. Until then the event still names
// the tool, and the missing frame is the failure.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

const chatLinksRequestID = "0123456789abcdef0123456789abcdef"

// chatLinksStampOpenTarget sets OpenInLibrary when the payload has that
// field. A missing field is not a failure here: the frame assertion is.
func chatLinksStampOpenTarget(payload *agent.ToolExecEndPayload) bool {
	field := reflect.ValueOf(payload).Elem().FieldByName("OpenInLibrary")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Pointer || field.Type().Elem().Kind() != reflect.Struct {
		return false
	}
	target := reflect.New(field.Type().Elem())
	for name, value := range map[string]string{
		"RequestID":   chatLinksRequestID,
		"WorkspaceID": "W",
		"Path":        "notes/plan.md",
	} {
		part := target.Elem().FieldByName(name)
		if !part.IsValid() || !part.CanSet() || part.Kind() != reflect.String {
			return false
		}
		part.SetString(value)
	}
	field.Set(target)
	return true
}

func chatLinksExecEnd(sessionID string, withTarget bool) agent.Event {
	payload := agent.ToolExecEndPayload{
		ToolCallID: "call-show",
		SessionID:  sessionID,
		Tool:       "show_to_user",
		Result:     "Asked the Library to open plan.md.\nlink: omnipus://file/W/notes/plan.md",
		AgentID:    "mia",
	}
	if withTarget {
		chatLinksStampOpenTarget(&payload)
	}
	return agent.Event{Kind: agent.EventKindToolExecEnd, Payload: payload}
}

type chatLinksRejectOpen struct {
	fakeHubConn
	rejected int
}

func (c *chatLinksRejectOpen) enqueue(frame []byte) bool {
	if bytes.Contains(frame, []byte(`"open_in_library"`)) {
		c.rejected++
		return false
	}
	return c.fakeHubConn.enqueue(frame)
}

func chatLinksFrames(wc *wsConn) []map[string]any {
	deadline := time.Now().Add(200 * time.Millisecond)
	var raw [][]byte
	for {
		select {
		case frame := <-wc.sendCh:
			raw = append(raw, frame)
		default:
			if time.Now().After(deadline) {
				out := make([]map[string]any, 0, len(raw))
				for _, frame := range raw {
					var decoded map[string]any
					if json.Unmarshal(frame, &decoded) == nil {
						out = append(out, decoded)
					}
				}
				return out
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func chatLinksOpenFrames(frames []map[string]any) []map[string]any {
	var out []map[string]any
	for _, frame := range frames {
		if frame["type"] == "open_in_library" {
			out = append(out, frame)
		}
	}
	return out
}

func TestHubToolExecEnd_ShowToUser_DeliversLiveOnly(t *testing.T) {
	reg := newHubRegistry("chat-links-boot")
	handler := &WSHandler{hubs: reg}
	const sid = "root-sess"
	hub := reg.getOrCreate(sid)
	boundA := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	boundB := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	held := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	unbound := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	held.startHold()
	if _, ok := hub.bindLive(boundA); !ok {
		t.Fatal("bindLive A")
	}
	if _, ok := hub.bindLive(boundB); !ok {
		t.Fatal("bindLive B")
	}
	if _, ok := hub.bindLive(held); !ok {
		t.Fatal("bindLive held")
	}
	reject := &chatLinksRejectOpen{fakeHubConn: fakeHubConn{name: "overflow"}}
	if _, ok := hub.bindLive(reject); !ok {
		t.Fatal("bindLive overflow")
	}
	var overflowed int
	hub.onOverflow = func(hubConn) { overflowed++ }

	const evictedSid = "evicted-sess"
	evictedHub := reg.getOrCreate(evictedSid)
	evictedConn := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
	if _, ok := evictedHub.bindLive(evictedConn); !ok {
		t.Fatal("bindLive evicted")
	}
	evictedHub.mu.Lock()
	evictedHub.evicted = true
	evictedHub.mu.Unlock()
	reg.mu.Lock()
	delete(reg.m, evictedSid)
	reg.mu.Unlock()

	headBefore := hub.head
	handler.hubToolExecEnd(chatLinksExecEnd(sid, true))
	handler.hubToolExecEnd(chatLinksExecEnd(evictedSid, true))

	opensA := chatLinksOpenFrames(chatLinksFrames(boundA))
	opensB := chatLinksOpenFrames(chatLinksFrames(boundB))
	if len(opensA) != 1 || len(opensB) != 1 {
		t.Fatalf("bound tabs got %d and %d open_in_library frames, want 1 each (FR-016 / BS-6.12)", len(opensA), len(opensB))
	}
	for _, frame := range []map[string]any{opensA[0], opensB[0]} {
		if _, hasSeq := frame["seq"]; hasSeq {
			t.Fatalf("open_in_library is sequenced: %v (FR-016)", frame)
		}
		if frame["session_id"] != sid || frame["workspace_id"] != "W" || frame["path"] != "notes/plan.md" {
			t.Fatalf("open_in_library = %v, want session %s workspace W path notes/plan.md", frame, sid)
		}
		if frame["request_id"] != chatLinksRequestID || frame["call_id"] != "call-show" {
			t.Fatalf("open_in_library identity = %v", frame)
		}
	}
	if len(chatLinksOpenFrames(chatLinksFrames(unbound))) != 0 {
		t.Fatal("unbound connection received open_in_library (BS-6.12)")
	}
	if hub.head != headBefore+1 {
		t.Fatalf("hub head moved by %d, want +1 for tool_call_result only (unsequenced frame)", hub.head-headBefore)
	}
	for _, entry := range hub.journal {
		if bytes.Contains(entry.bytes, []byte(`"open_in_library"`)) {
			t.Fatalf("journal contains open_in_library: %s (FR-016 / SEC-8)", entry.bytes)
		}
	}
	beforeRelease := chatLinksOpenFrames(chatLinksFrames(held))
	if len(beforeRelease) != 0 {
		t.Fatal("held connection saw open_in_library before catch-up finished (FR-016 hold)")
	}
	held.releaseHold()
	if len(chatLinksOpenFrames(chatLinksFrames(held))) != 1 {
		t.Fatal("held connection did not receive open_in_library after releaseHold (FR-016)")
	}
	if reject.rejected != 1 || overflowed != 1 {
		t.Fatalf("overflow reject=%d onOverflow=%d, want 1 and 1 (FR-016)", reject.rejected, overflowed)
	}
	// hubToolExecEnd publishes tool_call_result through getOrCreate before
	// the live frame. That existing publish creates a hub, so lookup after
	// the call is not evidence that deliverLive created one. The evicted
	// connection must still receive no open_in_library frame.
	if len(chatLinksOpenFrames(chatLinksFrames(evictedConn))) != 0 {
		t.Fatal("evicted hub delivered open_in_library (FR-016)")
	}
	// deliverLive itself looks up and never creates. An unknown session
	// stays absent; an evicted hub still in the map drops the frame and
	// is not replaced.
	const absentSid = "absent-sess"
	if n := reg.deliverLive(absentSid, []byte(`{"type":"open_in_library"}`)); n != 0 || reg.lookup(absentSid) != nil {
		t.Fatalf("deliverLive to a missing hub delivered %d and lookup=%v, want 0 and nil (FR-016)", n, reg.lookup(absentSid))
	}
	evictedOnly := reg.getOrCreate("evicted-only")
	evictedOnlyConn := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
	if _, ok := evictedOnly.bindLive(evictedOnlyConn); !ok {
		t.Fatal("bindLive evicted-only")
	}
	evictedOnly.mu.Lock()
	evictedOnly.evicted = true
	evictedOnly.mu.Unlock()
	if n := reg.deliverLive("evicted-only", []byte(`{"type":"open_in_library","session_id":"evicted-only"}`)); n != 0 {
		t.Fatalf("deliverLive on an evicted hub delivered %d, want 0 (FR-016)", n)
	}
	if reg.lookup("evicted-only") != evictedOnly {
		t.Fatal("deliverLive replaced an evicted hub via getOrCreate (FR-016)")
	}
	if len(chatLinksOpenFrames(chatLinksFrames(evictedOnlyConn))) != 0 {
		t.Fatal("evicted hub delivered open_in_library via deliverLive (FR-016)")
	}

	since := int64(headBefore)
	boot := reg.bootID
	late := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
	attached := hub.bind(late, &since, &boot, boot)
	var sawResult, sawOpen bool
	for _, frame := range attached.Tail {
		if bytes.Contains(frame, []byte(`"tool_call_result"`)) {
			sawResult = true
		}
		if bytes.Contains(frame, []byte(`"open_in_library"`)) {
			sawOpen = true
		}
	}
	if !sawResult {
		t.Fatal("instrument: incremental tail is missing the journaled tool_call_result")
	}
	if sawOpen {
		t.Fatal("incremental tail contains open_in_library (SEC-8)")
	}
}

func TestOpenInLibrary_NeverReplayedOrJournaled(t *testing.T) {
	fixture := newAttachFixture(t, "chat-links-turn", "chat-links-msg")
	mediaStore := newTestFileMediaStore(t)
	fixture.h.agentLoop.SetMediaStore(mediaStore)
	picture := filepath.Join(t.TempDir(), "plan.png")
	historyCrossGroupPNG(t, picture)
	ref, err := mediaStore.Store(picture, media.MediaMeta{
		Filename: "plan.png", ContentType: "image/png", CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, media.SessionInlineScopePrefix+fixture.sid)
	require.NoError(t, err)
	historyCrossGroupEntry(t, fixture.store, fixture.sid, "media-row", "media-call", ref, "plan.png")
	require.NoError(t, fixture.store.AppendTranscriptStrict(fixture.sid, session.TranscriptEntry{
		ID: "show-row", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia",
		Timestamp: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{{
			ID: "show-call", Tool: "show_to_user", Status: "success",
			Parameters: map[string]any{"ref": "notes/plan.md"},
			Result:     map[string]any{"for_llm": "Asked the Library to open plan.md."},
		}},
	}))

	viewer := fixture.newConn("chat-links-viewer")
	fixture.h.handleAttachSession(context.Background(), "chat-links-viewer", fixture.sid, nil, viewer)
	frames := pagingDrain(t, viewer)
	mediaN, openN := 0, 0
	for _, frame := range frames {
		switch frame.Type {
		case "media":
			mediaN++
		case "open_in_library":
			openN++
		}
	}
	if mediaN == 0 {
		t.Fatal("instrument: snapshot replay has no send_file media frame, so a missing open_in_library would be vacuous (test 15)")
	}
	if openN != 0 {
		t.Fatalf("snapshot delivered %d open_in_library frames, want 0 (BS-6.7 / SEC-8)", openN)
	}

	api := pagingAPIForAttach(fixture)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+fixture.sid+"/messages", nil)
	api.serveSessionHistory(recorder, req.WithContext(context.Background()), fixture.sid)
	if recorder.Code != http.StatusOK {
		t.Fatalf("history page status = %d body %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "open_in_library") {
		t.Fatal("REST history page contains open_in_library (SEC-8)")
	}
}
