package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/stretchr/testify/require"
)

// #1332 room history — second Go increment: the whole-history walk (T04),
// the attach cap boundary through the REAL attach (T08), auth-first
// ordering and access oracles on the real boot (T12/T13), and a real
// storage-fault oracle with a healthy control (T15). Oracles: ruling
// D2/D3/D5 and the boundary table; expected pages come from the seeded
// fixture lists. RED at merge 09ebe7593 (route 405); T12/T13's 401 leg is
// an auth-first order guard that passes as soon as auth wraps the route.

// l1332bAttach drives the REAL room-service attach — the exact entry the
// WebSocket room handler drives — and returns the snapshot.
func l1332bAttach(t *testing.T, rh *roomsHarness, roomID string, afterSeq int64) gen.RoomSnapshotFrame {
	t.Helper()
	var snap gen.RoomSnapshotFrame
	require.NoError(t, rh.hub.svc.Attach(roomID, afterSeq, func(frame gen.RoomSnapshotFrame) {
		snap = frame
	}))
	require.True(t, snap.Complete, "K45: healthy catch-up complete")
	require.NotNil(t, snap.Entries)
	require.NotNil(t, snap.MemberStates)
	return snap
}

// l1332bWalkAll pages backward with limit 1000 until has_older is false and
// returns every entry in ascending order — the client's own walk, judged
// against the seeded list, never against service internals.
func l1332bWalkAll(t *testing.T, rh *roomsHarness, roomID string) []gen.RoomEntry {
	t.Helper()
	// Pages arrive newest-first; PREPEND each older page so the union is
	// ascending. cursor tracks the previously used boundary for the
	// strictly-decreasing check; it is only set after the first page, so
	// there is no nil dereference on the first pass.
	var walked []gen.RoomEntry
	cursor := (*int64)(nil)
	for i := 0; i < 32; i++ {
		query := "?limit=1000"
		if cursor != nil {
			query = fmt.Sprintf("?before_seq=%d&limit=1000", *cursor)
		}
		page := l1332GetPage(t, rh, roomID, query)
		require.NotEmpty(t, page.Entries, "a walk must terminate by pages, not loop")
		walked = append(page.Entries, walked...)
		if !page.HasOlder {
			require.Nil(t, page.NextBeforeSeq)
			return walked
		}
		require.NotNil(t, page.NextBeforeSeq)
		next := *page.NextBeforeSeq
		if cursor != nil {
			require.Less(t, next, *cursor, "the cursor must strictly decrease")
		}
		cursor = &next
	}
	t.Fatal("the walk exceeded 32 pages for 10,500 entries")
	return nil
}

func TestIssue1332RoomHistory_WalkReachesTheFirstOfTenThousandFiveHundred(t *testing.T) {
	rh := newRoomsHarness(t)
	roomID := l1332NewSquadRoom(t, rh, "history-walk")
	l1332SeedDense(t, rh, roomID, 10500)

	// Start from attach's newest 10,000 (T04's own starting point), then
	// walk back until the start and check the union against the seed.
	snap := l1332bAttach(t, rh, roomID, 0)
	require.Len(t, snap.Entries, 10000, "the attach cap is unchanged by paging")
	require.Equal(t, "entry 501", snap.Entries[0].Text)
	require.Equal(t, "entry 10500", snap.Entries[len(snap.Entries)-1].Text)

	walked := l1332bWalkAll(t, rh, roomID)
	require.Len(t, walked, 10500, "every seeded entry exactly once")
	for i, entry := range walked {
		require.Equal(t, int64(i+1), entry.Seq, "ascending, no gaps, no duplicates")
		require.Equal(t, fmt.Sprintf("entry %d", i+1), entry.Text)
	}

	// The newest tail is unchanged by the walk.
	fresh := l1332bAttach(t, rh, roomID, 0)
	require.Len(t, fresh.Entries, 10000)
	require.Equal(t, snap.Entries[0].Text, fresh.Entries[0].Text)
	require.Equal(t, snap.Entries[len(snap.Entries)-1].Text, fresh.Entries[len(fresh.Entries)-1].Text)
}

func TestIssue1332RoomHistory_AttachCapBoundaryUnchanged(t *testing.T) {
	rh := newRoomsHarness(t)
	for _, count := range []int{0, 1, 9999, 10000, 10001} {
		roomID := l1332NewSquadRoom(t, rh, fmt.Sprintf("history-capattach-%d", count))
		l1332SeedDense(t, rh, roomID, count)
		snap := l1332bAttach(t, rh, roomID, 0)
		want := count
		if want > 10000 {
			want = 10000
		}
		require.Len(t, snap.Entries, want, "count=%d", count)
		if want > 0 {
			require.Equal(t, fmt.Sprintf("entry %d", count-want+1), snap.Entries[0].Text, "count=%d: the newest suffix", count)
			require.Equal(t, fmt.Sprintf("entry %d", count), snap.Entries[len(snap.Entries)-1].Text, "count=%d", count)
		}
		if count == 10001 {
			// The omitted first entry is reachable with the new GET.
			page := l1332GetPage(t, rh, roomID, fmt.Sprintf("?before_seq=%d", snap.Entries[0].Seq))
			require.Equal(t, []string{"entry 1"}, l1332Texts(t, page))
			require.False(t, page.HasOlder)
		}
	}
}

// l1332bAuthHarness boots the real gateway (auth users configured) for the
// auth-first and access oracles.
func l1332bAuthHarness(t *testing.T) *wu15RoomHarness {
	return wu15NewHarness(t)
}

func TestIssue1332RoomHistory_AuthFirstAndMissingRoom(t *testing.T) {
	h := l1332bAuthHarness(t)
	// Auth-first: an invalid token is refused with the existing 401 before
	// any query validation or page data, whatever the query carries.
	req, err := http.NewRequest(http.MethodGet, h.baseURL+"/api/v1/rooms/nope/messages?limit=abc&before_seq=zzz", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	body, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"auth-first: 401 before query validation; body=%s", body)

	// Valid credentials, missing room: the existing 404 body.
	status, data := h.http(http.MethodGet, "/api/v1/rooms/nope/messages", nil)
	require.Equal(t, http.StatusNotFound, status, "missing room: %s", data)
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(data, &errResp))
	require.Equal(t, "no such group chat", errResp.Error)
}

func TestIssue1332RoomHistory_StorageFaultIsAnErrorNeverAnEmptyPage(t *testing.T) {
	rh := newRoomsHarness(t)
	roomID := l1332NewSquadRoom(t, rh, "history-fault")
	l1332SeedDense(t, rh, roomID, 5)

	// Healthy control: the same room pages before the fault.
	page := l1332GetPage(t, rh, roomID, "")
	require.Len(t, page.Entries, 5)

	// Fault the real read seam the open path reaches: deny READ on the
	// room's journal directory for this process's user.
	journalDir := filepath.Join(rooms.DefaultDir(rh.api.homePath), roomID)
	require.NoError(t, os.Chmod(journalDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(journalDir, 0o755) })

	// The cached room still serves its in-memory view... a FRESH open (the
	// production restart shape this pack's seeding uses) must fail loudly.
	rh.hub = newRoomsHub(rh.h, rh.api.homePath)
	rh.api.rooms.Store(rh.hub)
	status, body := l1332Get(rh, roomID, "")
	require.Equal(t, http.StatusInternalServerError, status,
		"a real read fault is a 500, never a fabricated empty page; body=%s", body.Body.String())
	require.NotEmpty(t, body.Body.String())

	// Recovery: restoring the permission restores the page.
	require.NoError(t, os.Chmod(journalDir, 0o755))
	recovered := l1332GetPage(t, rh, roomID, "")
	require.Len(t, recovered.Entries, 5)
}
