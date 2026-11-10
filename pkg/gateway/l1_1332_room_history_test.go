package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/stretchr/testify/require"
)

// #1332 room history paging — server RED pack. Oracles are the binding
// ruling ROOM-HISTORY-PAGING-1332-20261018 (D1–D7) and the contract handoff
// 8a23e8b8b (getRoomHistory + RoomHistoryResponse); expected pages come from
// the seeded fixture lists and the ruling's numbers, never from the
// implementation. Real boundary throughout: roomsHarness drives the real
// HandleRooms dispatcher, the real rooms.Service and its file journals;
// bulk seeding posts through the real Service.PostMessage.
//
// RED at merge 09ebe7593 (2811dab68 + 8a23e8b8b): HandleRooms has no GET on
// the messages subpath, so every call lands on
// 405 {"error":"method not allowed"} — the endpoint this pack specifies
// does not exist yet. Each test below fails on that, never on a compile
// error. GREEN and the ruling's mutation list belong to independent CHECK.

// l1332Get calls the new history route and returns status + raw response.
func l1332Get(rh *roomsHarness, roomID, query string) (int, *httptest.ResponseRecorder) {
	rec := rh.do(http.MethodGet, "/api/v1/rooms/"+roomID+"/messages"+query, "", rh.api.HandleRooms)
	return rec.Code, rec
}

// l1332GetPage is l1332Get + l1332Page for the success path.
func l1332GetPage(t *testing.T, rh *roomsHarness, roomID, query string) gen.RoomHistoryResponse {
	t.Helper()
	status, body := l1332Get(rh, roomID, query)
	return l1332Page(t, status, body)
}

// l1332Page decodes a 200 body into the generated page.
func l1332Page(t *testing.T, status int, body *httptest.ResponseRecorder) gen.RoomHistoryResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, status, "history GET must succeed; body=%s", body.Body.String())
	var page gen.RoomHistoryResponse
	require.NoError(t, json.Unmarshal(body.Body.Bytes(), &page))
	return page
}

// l1332NewSquadRoom creates a fresh squad room and returns its id.
func l1332NewSquadRoom(t *testing.T, rh *roomsHarness, name string) string {
	t.Helper()
	w := rh.do(http.MethodPost, "/api/v1/workspaces/sales/rooms",
		fmt.Sprintf(`{"kind":"squad","name":%q,"agent_ids":["mia","jim"]}`, name), rh.api.HandleWorkspaces)
	require.Equal(t, http.StatusCreated, w.Code, "room create: %s", w.Body.String())
	var room gen.Room
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &room))
	require.NotEmpty(t, room.Id)
	return room.Id
}

// l1332Post seeds one message through the real service append.
func l1332Post(t *testing.T, rh *roomsHarness, roomID, clientID, text string) {
	t.Helper()
	require.NoError(t, rh.hub.svc.PostMessage(roomID, gen.RoomMessageRequest{
		ClientMessageId: clientID, Text: text, Mentions: []string{},
	}, "seeder"))
}

// l1332StoredEntry is the journal's own on-disk entry line shape
// (pkg/rooms storedEntry): the durable store boundary the real service
// parses and validates when it opens the room.
type l1332StoredEntry struct {
	Entry gen.RoomEntry `json:"entry"`
}

// l1332SeedDense seeds count plainly-numbered entries as dense entry seqs
// 1…count by writing the room journal's REAL entry records in one batch —
// the same records, file and layout Service.PostMessage appends — and
// letting the real service parse and validate them when it opens the room
// (its replay advances the stream position to the newest entry seq). A
// per-message service call would fsync 10,000+ times for fixture
// construction; the store boundary, not a mock, is still what the tests
// read back. Per-test behaviour messages keep going through the real
// service (l1332Post).
func l1332SeedDense(t *testing.T, rh *roomsHarness, roomID string, count int) {
	t.Helper()
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for i := 1; i <= count; i++ {
		cid := fmt.Sprintf("m%d", i)
		require.NoError(t, enc.Encode(l1332StoredEntry{Entry: gen.RoomEntry{
			Id: fmt.Sprintf("entry-%s-%d", roomID, i), RoomId: roomID,
			ClientMessageId: &cid, Text: fmt.Sprintf("entry %d", i),
			Author:     gen.RoomEntryAuthor{Kind: "user"},
			Addressees: []string{}, Deliveries: []gen.RoomDeliveryState{},
			Seq:       int64(i),
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		}}))
	}
	dir := filepath.Join(rooms.DefaultDir(rh.api.homePath), roomID)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, rooms.StreamEntries+".jsonl"), []byte(b.String()), 0o600))
	// The live service caches opened rooms and replays the journal only on
	// first load — serve the seeded room through a FRESH hub over the same
	// home, the production restart shape (never a per-request journal read).
	rh.hub = newRoomsHub(rh.h, rh.api.homePath)
	rh.api.rooms.Store(rh.hub)
}

// l1332Texts extracts the page's entry texts in returned order.
func l1332Texts(t *testing.T, page gen.RoomHistoryResponse) []string {
	t.Helper()
	out := make([]string, 0, len(page.Entries))
	for _, e := range page.Entries {
		out = append(out, e.Text)
	}
	return out
}

// l1332RequireContinuation asserts the ruling's conditional-continuation
// rule: next_before_seq present iff has_older, equal to the first entry's
// seq; omitted (never null/zero) otherwise.
func l1332RequireContinuation(t *testing.T, page gen.RoomHistoryResponse) {
	t.Helper()
	require.NotNil(t, page.Entries, "entries must be present and non-null")
	if page.HasOlder {
		require.NotNil(t, page.NextBeforeSeq, "has_older true requires next_before_seq")
		require.Equal(t, page.Entries[0].Seq, *page.NextBeforeSeq,
			"next_before_seq must equal the first returned entry's seq")
	} else {
		require.Nil(t, page.NextBeforeSeq, "has_older false must omit next_before_seq entirely")
	}
}

// pageHasOlder reads the page's has_older flag.
func pageHasOlder(page gen.RoomHistoryResponse) bool { return page.HasOlder }

// l1332ToggleJim consumes at least one non-entry stream sequence through the
// real member-mute path (ruling D2: state/meta changes consume seqs), so the
// next entry's seq is not consecutive with the previous one.
func l1332ToggleJim(t *testing.T, rh *roomsHarness, roomID, action string) {
	t.Helper()
	w := rh.do(http.MethodPatch, "/api/v1/rooms/"+roomID+"/members",
		fmt.Sprintf(`{"action":%q,"agent_ids":["jim"]}`, action), rh.api.HandleRooms)
	require.Equal(t, http.StatusOK, w.Code, "seq burn via members PATCH: %s", w.Body.String())
}

func TestIssue1332RoomHistory_RouteReturnsGeneratedPage(t *testing.T) {
	rh := newRoomsHarness(t)
	roomID := l1332NewSquadRoom(t, rh, "history-route")
	l1332Post(t, rh, roomID, "c1", "only entry")

	status, body := l1332Get(rh, roomID, "")
	page := l1332Page(t, status, body)
	require.Len(t, page.Entries, 1)
	require.Equal(t, "only entry", page.Entries[0].Text)
	require.False(t, page.HasOlder)
	l1332RequireContinuation(t, page)

	// Control: the existing POST keeps its unchanged 202 acceptance.
	w := rh.do(http.MethodPost, "/api/v1/rooms/"+roomID+"/messages",
		`{"client_message_id":"c2","text":"after get","mentions":[]}`, rh.api.HandleRooms)
	require.Equal(t, http.StatusAccepted, w.Code, "existing POST must be unchanged: %s", w.Body.String())
}

func TestIssue1332RoomHistory_DefaultNewestPageOfTenThousandFiveHundred(t *testing.T) {
	rh := newRoomsHarness(t)
	roomID := l1332NewSquadRoom(t, rh, "history-dense")
	l1332SeedDense(t, rh, roomID, 10500)

	page := l1332GetPage(t, rh, roomID, "")
	require.Len(t, page.Entries, 100, "default limit is 100")
	require.Equal(t, "entry 10401", page.Entries[0].Text)
	require.Equal(t, "entry 10500", page.Entries[len(page.Entries)-1].Text)
	for i := 1; i < len(page.Entries); i++ {
		require.Less(t, page.Entries[i-1].Seq, page.Entries[i].Seq, "strictly ascending seq")
	}
	require.True(t, page.HasOlder)
	require.NotNil(t, page.NextBeforeSeq)
	require.Equal(t, int64(10401), *page.NextBeforeSeq)
	l1332RequireContinuation(t, page)
}

func TestIssue1332RoomHistory_BeforeCursorIsExclusive(t *testing.T) {
	rh := newRoomsHarness(t)
	roomID := l1332NewSquadRoom(t, rh, "history-exclusive")
	l1332SeedDense(t, rh, roomID, 10500)

	page := l1332GetPage(t, rh, roomID, "?before_seq=10401&limit=100")
	require.Len(t, page.Entries, 100)
	require.Equal(t, "entry 10301", page.Entries[0].Text)
	require.Equal(t, "entry 10400", page.Entries[len(page.Entries)-1].Text,
		"the boundary entry 10401 is not repeated and 10400 is not skipped")
	seen := map[string]bool{}
	for _, e := range page.Entries {
		require.False(t, seen[e.Text], "no duplicate rows on one page")
		seen[e.Text] = true
	}
	require.True(t, page.HasOlder)
	require.NotNil(t, page.NextBeforeSeq)
	require.Equal(t, int64(10301), *page.NextBeforeSeq)
}

func TestIssue1332RoomHistory_SparseSequencesGappedEntries(t *testing.T) {
	rh := newRoomsHarness(t)
	roomID := l1332NewSquadRoom(t, rh, "history-sparse")

	// Seed three entries with real stream-seq gaps: a members PATCH consumes
	// non-entry sequences (ruling D2: state/meta changes consume seqs), so
	// consecutive entry seqs differ by more than one. One further toggle
	// after the last entry guarantees the stream's high-water is beyond the
	// newest entry seq, so newest+1 is an in-range cursor for the 400 case.
	for i := 1; i <= 3; i++ {
		l1332Post(t, rh, roomID, fmt.Sprintf("s%d", i), fmt.Sprintf("sparse %d", i))
		if i < 3 {
			if i%2 == 1 {
				l1332ToggleJim(t, rh, roomID, "mute")
			} else {
				l1332ToggleJim(t, rh, roomID, "unmute")
			}
		}
	}
	l1332ToggleJim(t, rh, roomID, "mute")

	// The page itself carries the fixture's actual entry seqs — the same
	// returned boundaries a paging client reuses (D2). Expected MEMBERSHIP
	// is the seeded text list; the seqs are fixture facts, not oracles.
	full := l1332GetPage(t, rh, roomID, "?limit=10")
	require.Equal(t, []string{"sparse 1", "sparse 2", "sparse 3"}, l1332Texts(t, full))
	require.False(t, pageHasOlder(full), "all three entries on one page")
	first, middle, newest := full.Entries[0].Seq, full.Entries[1].Seq, full.Entries[2].Seq
	require.Greater(t, middle, first+1, "fixture precondition: a real gap after the first entry")
	require.Greater(t, newest, middle+1, "fixture precondition: a real gap after the second entry")

	// No cursor, limit 2 → the newest two, ascending.
	page := l1332GetPage(t, rh, roomID, "?limit=2")
	require.Equal(t, []string{"sparse 2", "sparse 3"}, l1332Texts(t, page))
	require.True(t, page.HasOlder)
	require.NotNil(t, page.NextBeforeSeq)
	require.Equal(t, middle, *page.NextBeforeSeq)

	// Before the newest → the older two, terminal.
	page = l1332GetPage(t, rh, roomID, fmt.Sprintf("?before_seq=%d", newest))
	require.Equal(t, []string{"sparse 1", "sparse 2"}, l1332Texts(t, page))
	require.False(t, page.HasOlder)
	l1332RequireContinuation(t, page)

	// Before the middle → only the oldest.
	page = l1332GetPage(t, rh, roomID, fmt.Sprintf("?before_seq=%d&limit=2", middle))
	require.Equal(t, []string{"sparse 1"}, l1332Texts(t, page))
	require.False(t, page.HasOlder)
	l1332RequireContinuation(t, page)

	// Before the oldest → the terminal empty page.
	page = l1332GetPage(t, rh, roomID, fmt.Sprintf("?before_seq=%d", first))
	require.Empty(t, page.Entries)
	require.False(t, page.HasOlder)
	l1332RequireContinuation(t, page)

	// A cursor beyond the newest ENTRY is a 400 even though the stream's
	// high-water seq is higher still (D2: newest means newest entry).
	status, body := l1332Get(rh, roomID, fmt.Sprintf("?before_seq=%d", newest+1))
	require.Equal(t, http.StatusBadRequest, status, "body=%s", body.Body.String())
	require.JSONEq(t, `{"error":"before_seq must not be greater than the newest entry seq"}`, body.Body.String())
}

func TestIssue1332RoomHistory_LimitCaps(t *testing.T) {
	rh := newRoomsHarness(t)

	room1000 := l1332NewSquadRoom(t, rh, "history-cap-1000")
	l1332SeedDense(t, rh, room1000, 1000)
	page := l1332GetPage(t, rh, room1000, "?limit=1000")
	require.Len(t, page.Entries, 1000)
	require.Equal(t, "entry 1", page.Entries[0].Text)
	require.False(t, page.HasOlder)
	l1332RequireContinuation(t, page)

	room1001 := l1332NewSquadRoom(t, rh, "history-cap-1001")
	l1332SeedDense(t, rh, room1001, 1001)
	page = l1332GetPage(t, rh, room1001, "?limit=1000")
	require.Len(t, page.Entries, 1000)
	require.Equal(t, "entry 2", page.Entries[0].Text)
	require.Equal(t, "entry 1001", page.Entries[len(page.Entries)-1].Text)
	require.True(t, page.HasOlder)
	require.NotNil(t, page.NextBeforeSeq)
	require.Equal(t, int64(2), *page.NextBeforeSeq)
	page = l1332GetPage(t, rh, room1001, "?before_seq=2")
	require.Equal(t, []string{"entry 1"}, l1332Texts(t, page))
	require.False(t, page.HasOlder)
	l1332RequireContinuation(t, page)

	// Exactly default-100 eligible vs 101 eligible.
	room100 := l1332NewSquadRoom(t, rh, "history-default-100")
	l1332SeedDense(t, rh, room100, 100)
	page = l1332GetPage(t, rh, room100, "")
	require.Len(t, page.Entries, 100)
	require.False(t, page.HasOlder)
	l1332RequireContinuation(t, page)

	room101 := l1332NewSquadRoom(t, rh, "history-default-101")
	l1332SeedDense(t, rh, room101, 101)
	page = l1332GetPage(t, rh, room101, "")
	require.Len(t, page.Entries, 100)
	require.Equal(t, "entry 2", page.Entries[0].Text)
	require.True(t, page.HasOlder)
	require.NotNil(t, page.NextBeforeSeq)
	require.Equal(t, int64(2), *page.NextBeforeSeq)
}

func TestIssue1332RoomHistory_EmptyRoomAndFirstEntry(t *testing.T) {
	rh := newRoomsHarness(t)

	empty := l1332NewSquadRoom(t, rh, "history-empty")
	page := l1332GetPage(t, rh, empty, "")
	require.Empty(t, page.Entries)
	require.NotNil(t, page.Entries, "entries present as [] — never null")
	require.False(t, page.HasOlder)
	l1332RequireContinuation(t, page)

	status, body := l1332Get(rh, empty, "?before_seq=1")
	require.Equal(t, http.StatusBadRequest, status, "an empty room's newest entry seq is 0")
	require.JSONEq(t, `{"error":"before_seq must not be greater than the newest entry seq"}`, body.Body.String())

	one := l1332NewSquadRoom(t, rh, "history-one")
	l1332Post(t, rh, one, "only", "the one entry")
	page = l1332GetPage(t, rh, one, "")
	require.Equal(t, []string{"the one entry"}, l1332Texts(t, page))
	require.False(t, page.HasOlder)
	l1332RequireContinuation(t, page)

	// A one-entry room's entry seq need not be 1: read the entry's real seq
	// from the page and assert both below-newest cursors give the terminal
	// page — a cursor equal to the entry's seq and the literal cursor 1.
	onePage := l1332GetPage(t, rh, one, "?limit=10")
	require.Len(t, onePage.Entries, 1)
	for _, cursor := range []int64{onePage.Entries[0].Seq, 1} {
		page = l1332GetPage(t, rh, one, fmt.Sprintf("?before_seq=%d", cursor))
		require.Empty(t, page.Entries)
		require.False(t, page.HasOlder)
		l1332RequireContinuation(t, page)
	}
}

func TestIssue1332RoomHistory_LimitBoundaries(t *testing.T) {
	rh := newRoomsHarness(t)

	room101 := l1332NewSquadRoom(t, rh, "history-limit-default")
	l1332SeedDense(t, rh, room101, 101)
	page := l1332GetPage(t, rh, room101, "")
	require.Len(t, page.Entries, 100, "omitted limit means the default 100")

	room := l1332NewSquadRoom(t, rh, "history-limit-one")
	l1332SeedDense(t, rh, room, 5)
	page = l1332GetPage(t, rh, room, "?limit=1")
	require.Len(t, page.Entries, 1)
	require.Equal(t, "entry 5", page.Entries[0].Text)
	require.True(t, page.HasOlder)

	room1000 := l1332NewSquadRoom(t, rh, "history-limit-max")
	l1332SeedDense(t, rh, room1000, 1000)
	page = l1332GetPage(t, rh, room1000, "?limit=1000")
	require.Len(t, page.Entries, 1000)

	// Leading zeroes parse as their decimal value (D3: only digits are legal
	// characters; leading zeroes may parse normally).
	page = l1332GetPage(t, rh, room, "?limit=0001")
	require.Len(t, page.Entries, 1, "limit=0001 parses as 1")
}

func TestIssue1332RoomHistory_LimitRefusals(t *testing.T) {
	rh := newRoomsHarness(t)
	roomID := l1332NewSquadRoom(t, rh, "history-limit-bad")
	l1332Post(t, rh, roomID, "c1", "one entry")

	const want = `{"error":"limit must be an integer from 1 to 1000"}`
	for _, bad := range []string{
		"", "0", "-1", "1001", "abc", "1.5", "1.0", "1e2",
		"%2B1", // encoded +1
		"%201", // leading whitespace
		"99999999999999999999999999",
	} {
		status, body := l1332Get(rh, roomID, "?limit="+bad)
		require.Equal(t, http.StatusBadRequest, status, "limit=%q: body=%s", bad, body.Body.String())
		require.JSONEq(t, want, body.Body.String(), "limit=%q", bad)
	}
	// Duplicate occurrences are refused even when identical.
	status, body := l1332Get(rh, roomID, "?limit=1&limit=1")
	require.Equal(t, http.StatusBadRequest, status, "body=%s", body.Body.String())
	require.JSONEq(t, want, body.Body.String())
}

func TestIssue1332RoomHistory_CursorRefusals(t *testing.T) {
	rh := newRoomsHarness(t)
	roomID := l1332NewSquadRoom(t, rh, "history-cursor-bad")
	l1332SeedDense(t, rh, roomID, 3)

	const syntax = `{"error":"before_seq must be a positive int64 integer"}`
	for _, bad := range []string{
		"", "0", "-1", "abc", "1.5", "1e2", "%201", "%2B1",
		"99999999999999999999999999",
	} {
		status, body := l1332Get(rh, roomID, "?before_seq="+bad)
		require.Equal(t, http.StatusBadRequest, status, "before_seq=%q: body=%s", bad, body.Body.String())
		require.JSONEq(t, syntax, body.Body.String(), "before_seq=%q", bad)
	}
	status, body := l1332Get(rh, roomID, "?before_seq=1&before_seq=1")
	require.Equal(t, http.StatusBadRequest, status, "body=%s", body.Body.String())
	require.JSONEq(t, syntax, body.Body.String())

	// Valid cursor beyond the newest ENTRY (newest is 3): exact ahead-400.
	status, body = l1332Get(rh, roomID, "?before_seq=4")
	require.Equal(t, http.StatusBadRequest, status, "body=%s", body.Body.String())
	require.JSONEq(t, `{"error":"before_seq must not be greater than the newest entry seq"}`, body.Body.String())

	// Cursor == newest excludes the newest entry.
	page := l1332GetPage(t, rh, roomID, "?before_seq=3")
	require.Equal(t, []string{"entry 1", "entry 2"}, l1332Texts(t, page))
}

func TestIssue1332RoomHistory_HistoryReadsDoNotMutateTheRoom(t *testing.T) {
	rh := newRoomsHarness(t)
	roomID := l1332NewSquadRoom(t, rh, "history-readonly")
	l1332SeedDense(t, rh, roomID, 7)

	// The real append still works after reads, on the same journal.
	w := rh.do(http.MethodPost, "/api/v1/rooms/"+roomID+"/messages",
		`{"client_message_id":"after","text":"entry 8","mentions":[]}`, rh.api.HandleRooms)
	require.Equal(t, http.StatusAccepted, w.Code, "body=%s", w.Body.String())

	// The new entry is visible to the newest page and the older page is
	// unchanged — one valid locked view per read.
	page := l1332GetPage(t, rh, roomID, "?limit=3")
	require.Equal(t, []string{"entry 6", "entry 7", "entry 8"}, l1332Texts(t, page))
	// D2: below the exclusive cursor the page is the newest min(limit,
	// eligible) entries — all five of 1..5, ascending, terminal.
	older := l1332GetPage(t, rh, roomID, fmt.Sprintf("?before_seq=%d", page.Entries[0].Seq))
	require.Equal(t, []string{"entry 1", "entry 2", "entry 3", "entry 4", "entry 5"}, l1332Texts(t, older))
	require.False(t, older.HasOlder)
	l1332RequireContinuation(t, older)
}
