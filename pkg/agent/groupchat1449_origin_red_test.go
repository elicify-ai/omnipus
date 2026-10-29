// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Oracles: docs/internal/specs/1449-group-chat-spec.md D23, D28, DS-8 rows
// 2, 3, 11 and 12, scenarios S-79, S-83, S-151.
// A card carries a group reference only when the step answers that one group
// and nothing else. Two groups, or any other input, keeps the card in the
// agent's own chat (no group reference).

func TestGroupChat1449_RoomOriginOnlyWhenTheStepAnswersThatGroupAlone(t *testing.T) {
	launch := &generated.RoomRef{RoomId: "launch", RoomName: "Launch", RequestId: "del-1", Role: "request"}
	other := &generated.RoomRef{RoomId: "roadmap", RoomName: "Roadmap", RequestId: "del-2", Role: "request"}
	room := approvalOrigin{Label: "for group Launch", Class: originRoom, RoomRef: launch}
	web := approvalOrigin{Label: "", Class: originWeb}
	channel := approvalOrigin{Label: "while answering Ada on telegram", Class: originChannel}
	second := approvalOrigin{Label: "for group Roadmap", Class: originRoom, RoomRef: other}

	cases := []struct {
		name    string
		current approvalOrigin
		next    approvalOrigin
		wantNil bool
	}{
		{name: "S-71 group work alone keeps the reference", current: room, next: room, wantNil: false},
		{name: "S-83 own-chat message then the group drops the reference", current: web, next: room, wantNil: true},
		{name: "S-83 group then an own-chat message drops the reference", current: room, next: web, wantNil: true},
		{name: "S-79 channel message drops the reference", current: room, next: channel, wantNil: true},
		{name: "S-79 group after a channel message drops the reference", current: channel, next: room, wantNil: true},
		{name: "S-151 two groups drop the reference", current: room, next: second, wantNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeApprovalOrigin(tc.current, tc.next)
			if tc.wantNil && got.RoomRef != nil {
				t.Fatalf("room reference = %+v, want none (card stays in the agent's own chat)", got.RoomRef)
			}
			if !tc.wantNil && (got.RoomRef == nil || got.RoomRef.RoomId != "launch") {
				t.Fatalf("room reference = %+v, want Launch", got.RoomRef)
			}
		})
	}
}
