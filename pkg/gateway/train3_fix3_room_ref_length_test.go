package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Gate round 3, security O1 (founder: fix now): room_ref.room_name is capped
// at 80 by the browser's generated validator, counted in UTF-16 units. The
// room-name rule now counts units at the source (pkg/rooms::roomName); this is
// the producer's safety net, the same rule as fitsWireLength for the S4
// fields: a room_ref whose name does not fit is omitted, the approval itself
// always arrives, and one such entry never costs the rest of a reload snapshot.

func train3Fix3RoomRequest(t *testing.T, h *WSHandler, callID, roomName string) *approvalEntry {
	t.Helper()
	ref := &generated.RoomRef{RoomId: "room-" + callID, RoomName: roomName, RequestId: "req-" + callID, Role: "request"}
	entry, accepted := h.approvalRegV2.requestApproval(callID, "send_message", map[string]any{"to": "mia"},
		"plain", "sess-"+callID, "turn-"+callID, approvalExtras{RoomRef: ref, OriginLabel: "for group x"})
	require.True(t, accepted)
	go func() { <-entry.resultCh }()
	t.Cleanup(func() { go func() { h.approvalRegV2.resolve(entry.ApprovalID, ApprovalActionCancel, false) }() })
	return entry
}

var (
	train3Fix3NameOver = strings.Repeat(train3Fix2Supplementary, 41) // 41 code points, 82 units
	train3Fix3NameAt   = strings.Repeat(train3Fix2Supplementary, 40) // exactly 80 units
)

func TestTrain3Fix3_LiveApprovalRoomRefFitsTheBrowserValidator(t *testing.T) {
	h, wc := train3Fix2Handler(t)
	for i, tc := range []struct{ name, roomName string }{
		{"over the limit is omitted", train3Fix3NameOver},
		{"exactly at the limit is kept", train3Fix3NameAt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := train3Fix3RoomRequest(t, h, "tc-ref-"+string(rune('a'+i)), tc.roomName)
			h.broadcastToolApprovalRequired(entry)
			var frame generated.ToolApprovalRequiredFrame
			select {
			case raw := <-wc.sendCh:
				require.NoError(t, json.Unmarshal(raw, &frame))
			case <-time.After(2 * time.Second):
				t.Fatal("no tool_approval_required frame: the approval itself must always arrive")
			}
			require.Equal(t, entry.ApprovalID, frame.ApprovalId)
			if tc.roomName == train3Fix3NameOver {
				require.Nil(t, frame.RoomRef, "a room_ref the browser would reject is omitted")
				return
			}
			require.NotNil(t, frame.RoomRef)
			require.Equal(t, tc.roomName, frame.RoomRef.RoomName)
			require.True(t, train3Fix2BrowserFits(frame.RoomRef.RoomName, 80))
		})
	}
}

func TestTrain3Fix3_ReloadSnapshotRoomRefKeepsEveryApproval(t *testing.T) {
	h, wc := train3Fix2Handler(t)
	over := train3Fix3RoomRequest(t, h, "tc-ref-snap-over", train3Fix3NameOver)
	at := train3Fix3RoomRequest(t, h, "tc-ref-snap-at", train3Fix3NameAt)
	valid := train3Fix3RoomRequest(t, h, "tc-ref-snap-valid", "Launch")

	var state generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(h.sessionStateBytes(wc, "sess-tc-ref-snap-valid"), &state))
	byID := map[string]generated.SessionStatePendingApproval{}
	for _, p := range state.PendingApprovals {
		byID[p.ApprovalId] = p
		if p.RoomRef != nil {
			require.True(t, train3Fix2BrowserFits(p.RoomRef.RoomName, 80),
				"approval %s: room_ref.room_name would make the browser reject the whole snapshot", p.ApprovalId)
		}
	}
	require.Len(t, byID, 3, "every pending approval is in the snapshot")
	require.Nil(t, byID[over.ApprovalID].RoomRef)
	require.NotNil(t, byID[at.ApprovalID].RoomRef)
	require.Equal(t, train3Fix3NameAt, byID[at.ApprovalID].RoomRef.RoomName)
	require.NotNil(t, byID[valid.ApprovalID].RoomRef)
	require.Equal(t, "Launch", byID[valid.ApprovalID].RoomRef.RoomName)
}
