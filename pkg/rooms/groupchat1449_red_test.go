// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/addressing"
)

// Oracles: docs/internal/specs/1449-group-chat-spec.md DS-1, S-18, S-115,
// S-118, S-119, S-150 and the machine-verifiable size rows. The effective
// group limit is min(steer_body, 65536). A larger operator value must not
// raise it (D27).

func TestGroupChat1449_PersonMessageSizeAtDefaultLimit(t *testing.T) {
	// DS-1 rows 18–21, S-118. Default steer body is 65,536.
	cases := []struct {
		name    string
		text    string
		status  int
		snippet string
	}{
		{name: "one byte", text: "a", status: 202},
		{name: "65536 ASCII", text: strings.Repeat("a", 65536), status: 202},
		{name: "65537 ASCII", text: strings.Repeat("a", 65537), status: 422, snippet: "65536"},
		{name: "21845 three-byte characters", text: strings.Repeat("界", 21845), status: 202},
		{name: "21846 three-byte characters", text: strings.Repeat("界", 21846), status: 422, snippet: "65536"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			room := f.squad("mia", "jim")
			before := groupChatEntryCount(t, f, room.Id)
			err := f.post(room.Id, "size-"+tc.name, tc.text)
			groupChatRequireStatus(t, err, tc.status, tc.snippet)
			after := groupChatEntryCount(t, f, room.Id)
			if tc.status == 202 && after != before+1 {
				t.Fatalf("S-118: accepted message was not stored (%d -> %d)", before, after)
			}
			if tc.status != 202 && after != before {
				t.Fatalf("S-118: refused message was stored (%d -> %d)", before, after)
			}
		})
	}
}

func TestGroupChat1449_OperatorSettingCannotRaiseTheGroupLimit(t *testing.T) {
	// DS-1 rows 26–31, S-150. deps.Limits is the operator steer_body the
	// gateway already passes through (gatewayRoomDeps.Limits).
	cases := []struct {
		name    string
		steer   int
		text    string
		status  int
		snippet string
	}{
		{name: "unset uses 65536", steer: 0, text: strings.Repeat("a", 65536), status: 202},
		{name: "131072 still accepts 65536", steer: 131072, text: strings.Repeat("a", 65536), status: 202},
		{name: "131072 refuses 65537 naming 65536", steer: 131072, text: strings.Repeat("a", 65537), status: 422, snippet: "65536"},
		{name: "1000 accepts 1000", steer: 1000, text: strings.Repeat("a", 1000), status: 202},
		{name: "1000 refuses 1001 naming 1000", steer: 1000, text: strings.Repeat("a", 1001), status: 422, snippet: "1000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.deps.body = tc.steer
			room := f.squad("mia", "jim")
			before := groupChatEntryCount(t, f, room.Id)
			err := f.post(room.Id, "steer", tc.text)
			groupChatRequireStatus(t, err, tc.status, tc.snippet)
			after := groupChatEntryCount(t, f, room.Id)
			if tc.status == 202 && after != before+1 {
				t.Fatalf("S-150: accepted message was not stored")
			}
			if tc.status != 202 && after != before {
				t.Fatalf("S-150: refused message was stored (%d -> %d)", before, after)
			}
		})
	}
}

func TestGroupChat1449_EmptyAndWhitespacePersonMessageIsRefused(t *testing.T) {
	// DS-1 rows 1–3, S-18. Nothing is appended.
	f := newFixture(t)
	room := f.squad("mia", "jim")
	for _, text := range []string{"", "   \n\t"} {
		before := groupChatEntryCount(t, f, room.Id)
		err := f.post(room.Id, "empty-"+text, text)
		groupChatRequireStatus(t, err, 422, "")
		if got := groupChatEntryCount(t, f, room.Id); got != before {
			t.Fatalf("S-18: empty message stored an entry (%d -> %d)", before, got)
		}
	}
	if err := f.post(room.Id, "one", "a"); err != nil {
		t.Fatalf("S-18: one character must be accepted, got %v", err)
	}
}

func TestGroupChat1449_MentionCountBounds(t *testing.T) {
	// DS-1 rows 14–15. 256 accepted, 257 is 400, nothing stored for the refusal.
	f := newFixture(t)
	room := f.squad("mia", "jim")
	ok := make([]string, 256)
	for i := range ok {
		ok[i] = "mia"
	}
	if err := f.post(room.Id, "mentions-256", "hello", ok...); err != nil {
		t.Fatalf("DS-1 row 14: 256 mentions must be accepted, got %v", err)
	}
	tooMany := append(append([]string{}, ok...), "mia")
	before := groupChatEntryCount(t, f, room.Id)
	err := f.post(room.Id, "mentions-257", "hello", tooMany...)
	groupChatRequireStatus(t, err, 400, "")
	if got := groupChatEntryCount(t, f, room.Id); got != before {
		t.Fatalf("DS-1 row 15: 257 mentions stored an entry")
	}
}

func TestGroupChat1449_AgentAnswerUsesTheClampedLimitAndPostsNothingWhenOver(t *testing.T) {
	// S-119, S-150 row 30. An over-limit answer is a refusal: nothing posted,
	// nothing mirrored, nothing passed on. The clamp is 65,536 even when the
	// operator setting is higher.
	f := newFixture(t)
	f.deps.body = 131072
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "ask", "@Jim the diagram"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	if !f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim") {
		t.Fatal("setup: the request must be consumable")
	}
	beforeEntries := groupChatEntryCount(t, f, room.Id)
	beforeMirrors := len(f.deps.mirrors)
	_, err := f.svc.DeliverAnswer(context.Background(), Answer{
		RoomID: room.Id, RequestID: jim.RequestID,
		Responder:          addressing.Pair{WorkspaceID: "sales", AgentID: "jim"},
		ResponderSessionID: "main-jim",
		Content:            strings.Repeat("a", 65537),
	})
	if err == nil || !strings.Contains(err.Error(), "65536") {
		t.Fatalf("S-119: answer error = %v, want a refusal naming the 65536-byte limit", err)
	}
	if got := groupChatEntryCount(t, f, room.Id); got != beforeEntries {
		t.Fatalf("S-119: over-limit answer was posted (%d -> %d)", beforeEntries, got)
	}
	if len(f.deps.mirrors) != beforeMirrors {
		t.Fatalf("S-119: over-limit answer was mirrored (%d -> %d)", beforeMirrors, len(f.deps.mirrors))
	}
}

func TestGroupChat1449_PausedMessageIsSavedAndNotDelivered(t *testing.T) {
	// S-115, US-15 acceptance scenario 2. Saved, not delivered.
	f := newFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "pause", "/pause"); err != nil {
		t.Fatalf("/pause: %v", err)
	}
	beforeAdmits := len(f.deps.admittedTo())
	if err := f.post(room.Id, "while-paused", "saved while paused"); err != nil {
		t.Fatalf("S-115: a message while paused must be saved, got %v", err)
	}
	if got := len(f.deps.admittedTo()); got != beforeAdmits {
		t.Fatalf("S-115: paused message was delivered, admissions %d -> %d", beforeAdmits, got)
	}
	page, err := f.svc.History(room.Id, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range page.Entries {
		if entry.Text == "saved while paused" {
			found = true
		}
	}
	if !found {
		t.Fatal("S-115: paused message was not in the group history")
	}
}

func TestGroupChat1449_SteeringCommandIsRefusedWithTheMemberLink(t *testing.T) {
	// S-13. Exact wording from the scenario: "Rooms don't steer agents - use Mia's main chat".
	// Nothing is posted.
	f := newFixture(t)
	room := f.squad("mia", "jim")
	before := groupChatEntryCount(t, f, room.Id)
	err := f.post(room.Id, "steer", "/model gpt @Mia")
	groupChatRequireStatus(t, err, 422, "Rooms don't steer agents - use Mia's main chat")
	if got := groupChatEntryCount(t, f, room.Id); got != before {
		t.Fatalf("S-13: a refused steering command was posted")
	}
}

func TestGroupChat1449_NewGroupExposesInvitationStatus(t *testing.T) {
	// S-61, Room.invitation_context_status. A new group has a status the panel
	// can show: generating, ready, or unavailable. Absent is not a status.
	f := newFixture(t)
	room := f.squad("mia", "jim")
	raw, err := json.Marshal(room)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	status, _ := obj["invitation_context_status"].(string)
	switch status {
	case "generating", "ready", "unavailable":
	default:
		t.Fatalf("invitation_context_status = %#v, want generating, ready or unavailable (spec Room schema, S-61)", obj["invitation_context_status"])
	}
}

func groupChatEntryCount(t *testing.T, f *fixture, roomID string) int {
	t.Helper()
	page, err := f.svc.History(roomID, nil, 100)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	return len(page.Entries)
}

func groupChatRequireStatus(t *testing.T, err error, status int, snippet string) {
	t.Helper()
	if status == 202 {
		if err != nil {
			t.Fatalf("status error = %v, want accepted (202)", err)
		}
		return
	}
	var roomErr *Error
	if !errors.As(err, &roomErr) {
		t.Fatalf("error = %v, want rooms.Error status %d", err, status)
	}
	if roomErr.Status != status {
		t.Fatalf("status = %d (%s), want %d", roomErr.Status, roomErr.Msg, status)
	}
	if snippet != "" && !strings.Contains(roomErr.Msg, snippet) {
		t.Fatalf("message %q does not contain %q", roomErr.Msg, snippet)
	}
}
