package rooms

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// Gate round 3, security O1 (founder: fix now): every wire carrier of a room
// name (Room.name, RoomRef.room_name) is capped at 80 by the browser's
// generated validator, which counts JavaScript String.length — UTF-16 code
// units. A character outside the Basic Multilingual Plane (U+20000) is two
// units. A name accepted at 80 CODE POINTS could therefore make the browser
// reject a whole approval frame, reload snapshot or room list.

const fix3Supplementary = "\U00020000"

func fix3Units(s string) int { return len(utf16.Encode([]rune(s))) }

func TestTrain3Fix3_RoomNameCountsUTF16Units(t *testing.T) {
	over := strings.Repeat(fix3Supplementary, 41) // 41 code points, 82 units
	if _, err := roomName(KindSquad, &over, nil, WorkspaceInfo{}); err == nil {
		t.Fatalf("a name of %d UTF-16 units must be refused (limit 80)", fix3Units(over))
	}
	at := strings.Repeat(fix3Supplementary, 40) // exactly 80 units
	got, err := roomName(KindSquad, &at, nil, WorkspaceInfo{})
	if err != nil || got != at {
		t.Fatalf("a name of exactly 80 UTF-16 units must be kept: %q, %v", got, err)
	}
	ascii := strings.Repeat("a", 80)
	if got, err := roomName(KindSquad, &ascii, nil, WorkspaceInfo{}); err != nil || got != ascii {
		t.Fatalf("control: 80 ASCII characters stay valid: %q, %v", got, err)
	}
}

func TestTrain3Fix3_ComposedAdhocNameFitsInUTF16Units(t *testing.T) {
	ws := WorkspaceInfo{Agents: []Agent{
		{ID: "a", Name: strings.Repeat(fix3Supplementary, 30)},
		{ID: "b", Name: strings.Repeat(fix3Supplementary, 30)},
	}}
	got, err := roomName(KindAdhoc, nil, []string{"a", "b"}, ws)
	if err != nil {
		t.Fatalf("an ad-hoc name is composed, never refused: %v", err)
	}
	if n := fix3Units(got); n > 80 {
		t.Fatalf("composed ad-hoc name is %d UTF-16 units, must be at most 80: %q", n, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a shortened composed name ends with an ellipsis: %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat(fix3Supplementary, 30)) {
		t.Fatalf("truncation keeps whole characters from the start: %q", got)
	}
}
