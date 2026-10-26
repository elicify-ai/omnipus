package gateway

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Oracle: chat-history-paging-spec.md D2/D3/D10, B01/B02/B17 and D-A.
// The handler, archive, index and projector are real. Expected IDs below are
// acceptance-order arithmetic, never a full reader used as the page oracle.
func pagingSeed(t *testing.T, store *session.UnifiedStore, sid, prefix string, n int) []session.ArchiveAddress {
	t.Helper()
	addresses := make([]session.ArchiveAddress, 0, n)
	for i := 1; i <= n; i++ {
		addr, err := store.AppendTranscriptAddressed(sid, session.TranscriptEntry{
			ID: fmt.Sprintf("%s-%03d", prefix, i), Role: "user", Content: fmt.Sprintf("row %s %d", prefix, i),
			AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
			ViewMembership: session.ViewMembershipChat,
		})
		require.NoError(t, err, "fixture append %d", i)
		addresses = append(addresses, addr)
	}
	return addresses
}

func pagingRequest(api *restAPI, sid, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages", nil)
	r.URL.RawQuery = query // Deliberately preserve bad percent encodings/multiplicity.
	api.HandleSessions(w, r)
	return w
}

func pagingRead(t *testing.T, api *restAPI, sid, query string) generated.SessionHistoryResponse {
	t.Helper()
	w := pagingRequest(api, sid, query)
	require.Equal(t, http.StatusOK, w.Code, "history page: %s", w.Body.String())
	var page generated.SessionHistoryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page), "D3 requires the generated page envelope, never the old full-history array")
	require.Equal(t, sid, page.SessionId, "same-session envelope")
	require.NotNil(t, page.Entries, "B02: empty history is [], not null")
	return page
}

func pagingIDs(page generated.SessionHistoryResponse) []string {
	ids := make([]string, 0, len(page.Entries))
	for _, entry := range page.Entries {
		ids = append(ids, entry.Id)
	}
	return ids
}

func pagingWantIDs(prefix string, first, last int) []string {
	ids := make([]string, 0)
	for i := first; i <= last; i++ {
		ids = append(ids, fmt.Sprintf("%s-%03d", prefix, i))
	}
	return ids
}

func pagingShape(t *testing.T, p generated.SessionHistoryResponse, earlier, later bool) {
	t.Helper()
	require.Equal(t, earlier, p.HasEarlier, "actual retained predecessor lookahead")
	require.Equal(t, later, p.HasLater, "actual retained successor lookahead")
	require.Equal(t, earlier, p.NextBefore != nil, "next_before iff has_earlier")
	require.Equal(t, later, p.NextAfter != nil, "next_after iff has_later")
	require.Equal(t, len(p.Entries), p.PageInfo.SourceGroupCount, "one selected entry per whole source group")
	require.EqualValues(t, 4194304, p.PageInfo.SoftByteBudget, "D4a server default, not a configurable or hard cap")
	require.NotEmpty(t, p.PageInfo.RetentionGeneration, "retained cut identity")
	require.NotNil(t, p.PageInfo.Context.Spans, "non-null relevant span context")
	require.NotNil(t, p.PageInfo.Context.DuplicateIds, "non-null duplicate context")
	if len(p.Entries) == 0 {
		require.Nil(t, p.PageInfo.FirstPosition, "empty page first_position omitted")
		require.Nil(t, p.PageInfo.LastPosition, "empty page last_position omitted")
		return
	}
	for _, e := range p.Entries {
		require.NotNil(t, e.HistoryPosition, "every paged occurrence has an origin: %s", e.Id)
		require.Regexp(t, `^hp1_[A-Za-z0-9_-]{43}$`, *e.HistoryPosition, "opaque 47-character occurrence")
	}
	require.Equal(t, p.Entries[0].HistoryPosition, p.PageInfo.FirstPosition, "first selected occurrence")
	require.Equal(t, p.Entries[len(p.Entries)-1].HistoryPosition, p.PageInfo.LastPosition, "last selected occurrence")
	if earlier {
		require.Equal(t, p.PageInfo.FirstPosition, p.NextBefore, "exclusive predecessor boundary")
	}
	if later {
		require.Equal(t, p.PageInfo.LastPosition, p.NextAfter, "exclusive successor boundary")
	}
}

func TestChatHistoryPage_SelectsNewestAndActualLookahead(t *testing.T) {
	cases := []struct {
		count, limit, returned int
		earlier                bool
	}{
		{0, 20, 0, false}, {1, 20, 1, false}, {19, 20, 19, false}, {20, 20, 20, false}, {21, 20, 20, true},
		{39, 40, 39, false}, {40, 40, 40, false}, {41, 40, 40, true},
		{99, 100, 99, false}, {100, 100, 100, false}, {101, 100, 100, true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("count%d_limit%d", tc.count, tc.limit), func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			sid := createTestSession(t, api)
			pagingSeed(t, api.agentLoop.GetSessionStore(), sid, "a", tc.count)
			p := pagingRead(t, api, sid, "limit="+strconv.Itoa(tc.limit))
			require.Equal(t, pagingWantIDs("a", tc.count-tc.returned+1, tc.count), pagingIDs(p), "B02 hand-computed newest ascending suffix")
			require.Equal(t, "latest", p.PageInfo.Mode)
			require.Equal(t, tc.limit, p.PageInfo.Limit)
			require.Equal(t, 20, p.PageInfo.MinRows, "geometry-unknown default floor")
			require.Equal(t, tc.returned, p.PageInfo.RowCount, "ordinary rows each weigh one")
			pagingShape(t, p, tc.earlier, false)
		})
	}
}

func TestChatHistoryPage_ExclusiveBeforeAfterAndClosedWindow(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	pagingSeed(t, api.agentLoop.GetSessionStore(), sid, "a", 100)
	latest := pagingRead(t, api, sid, "")
	require.Equal(t, pagingWantIDs("a", 61, 100), pagingIDs(latest), "D3 omitted limit is40")
	older := pagingRead(t, api, sid, "limit=20&before="+*latest.PageInfo.FirstPosition)
	require.Equal(t, pagingWantIDs("a", 41, 60), pagingIDs(older), "before excludes a-061")
	pagingShape(t, older, true, true)
	oldest := pagingRead(t, api, sid, "limit=20&before="+*older.PageInfo.FirstPosition)
	require.Equal(t, pagingWantIDs("a", 21, 40), pagingIDs(oldest))
	first := pagingRead(t, api, sid, "limit=20&before="+*oldest.PageInfo.FirstPosition)
	require.Equal(t, pagingWantIDs("a", 1, 20), pagingIDs(first))
	pagingShape(t, first, false, true)
	forward := pagingRead(t, api, sid, "limit=20&after="+*first.PageInfo.LastPosition)
	require.Equal(t, pagingWantIDs("a", 21, 40), pagingIDs(forward), "after excludes a-020 and picks nearest, not newest")
	require.Equal(t, "newer", forward.PageInfo.Mode)
	pagingShape(t, forward, true, true)
	bounds := "window_start=" + *oldest.PageInfo.FirstPosition + "&window_end=" + *older.PageInfo.LastPosition
	refresh := pagingRead(t, api, sid, "limit=20&"+bounds)
	require.Equal(t, pagingWantIDs("a", 41, 60), pagingIDs(refresh), "closed21..60 interval, newest20 first")
	require.Equal(t, "window_refresh", refresh.PageInfo.Mode)
	pagingShape(t, refresh, true, false)
	refresh = pagingRead(t, api, sid, "limit=20&"+bounds+"&before="+*refresh.NextBefore)
	require.Equal(t, pagingWantIDs("a", 21, 40), pagingIDs(refresh), "terminal range must not pull in a-020")
	pagingShape(t, refresh, false, true)
}

// Independent D2 test-side encoding. Golden constants in the core/command pack
// cross-check this reference; the product encoder is never an oracle here.
func pagingPositionOracle(sid string, a session.ArchiveAddress) string {
	h := sha256.New()
	for _, part := range []string{sid, a.PartitionKey, strconv.FormatInt(a.ByteOffset, 10), a.EntryID} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len([]byte(part))))
		_, _ = h.Write(n[:]) // hash.Hash.Write cannot fail.
		_, _ = h.Write([]byte(part))
	}
	return "hp1_" + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func TestChatHistoryPosition_StableOccurrenceAndSessionScope(t *testing.T) {
	pagingVerifyPositionVectors(t)
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	sid := createTestSession(t, api)
	addresses := pagingSeed(t, store, sid, "same-time", 21)
	p := pagingRead(t, api, sid, "limit=20")
	for i, e := range p.Entries {
		require.NotNil(t, e.HistoryPosition)
		require.Equal(t, pagingPositionOracle(sid, addresses[i+1]), *e.HistoryPosition, "D2 actual checked UTF-8 byte address, not timestamp/model ordinal")
	}
	before := *p.PageInfo.FirstPosition
	pagingSeed(t, store, sid, "later", 1)
	p2 := pagingRead(t, api, sid, "limit=100")
	require.Equal(t, before, *p2.Entries[1].HistoryPosition, "append never renumbers a saved occurrence")
	other := createTestSession(t, api)
	pagingSeed(t, store, other, "same-time", 1)
	w := pagingRequest(api, other, "before="+before)
	require.Equal(t, http.StatusNotFound, w.Code, "valid foreign token cannot authorize history")
	var refusal generated.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refusal))
	require.Equal(t, "history position not found", refusal.Error)
}

func pagingCanonicalBytes(t *testing.T, store *session.UnifiedStore, sid string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(store.BaseDir(), sid, "transcript*.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "instrument: fixture has real canonical content")
	out := make(map[string]string, len(files))
	for _, path := range files {
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		out[filepath.Base(path)] = string(b)
	}
	return out
}

func pagingMarker(t *testing.T, s *session.UnifiedStore, sid, id string) session.ArchiveAddress {
	t.Helper()
	a, err := s.AppendTranscriptAddressed(sid, session.TranscriptEntry{
		ID: id, Type: session.EntryTypeSystem, Role: "system", Content: session.ClearMarkerText,
		AgentID: "mia", ViewMembership: session.ViewMembershipChat,
		Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	return a
}

func TestChatHistoryPage_ClearStopsAndExplicitCrossing(t *testing.T) {
	for _, n := range []int{0, 1, 19, 20, 100} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			sid := createTestSession(t, api)
			s := api.agentLoop.GetSessionStore()
			pagingSeed(t, s, sid, "old", 250)
			c1 := pagingMarker(t, s, sid, "C1")
			pagingSeed(t, s, sid, "between", 60)
			c2 := pagingMarker(t, s, sid, "C2")
			pagingSeed(t, s, sid, "recent", n)
			canonical := pagingCanonicalBytes(t, s, sid)
			latest := pagingRead(t, api, sid, "limit=100")
			want := pagingWantIDs("recent", 1, n)
			if n < 100 {
				want = append([]string{"C2"}, want...)
			}
			require.Equal(t, want, pagingIDs(latest), "no unchosen clear crossing or distant marker across a gap")
			require.NotNil(t, latest.PageInfo.LatestClear)
			require.Equal(t, "C2", latest.PageInfo.LatestClear.EntryId)
			require.Equal(t, pagingPositionOracle(sid, c2), latest.PageInfo.LatestClear.HistoryPosition)
			prior := pagingRead(t, api, sid, "limit=100&before="+pagingPositionOracle(sid, c2))
			require.Equal(t, append([]string{"C1"}, pagingWantIDs("between", 1, 60)...), pagingIDs(prior), "explicit crossing opens one preceding segment only")
			pagingShape(t, prior, true, true)
			require.NotNil(t, prior.PageInfo.StoppedAtClear)
			require.True(t, *prior.PageInfo.StoppedAtClear)
			old := pagingRead(t, api, sid, "limit=20&before="+pagingPositionOracle(sid, c1))
			require.Equal(t, pagingWantIDs("old", 231, 250), pagingIDs(old))
			require.Equal(t, canonical, pagingCanonicalBytes(t, s, sid), "display crossing never rewrites archive/model context")
		})
	}
}

func TestChatHistoryPage_MarkerLikeTextDoesNotCut(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	s := api.agentLoop.GetSessionStore()
	for _, e := range []session.TranscriptEntry{
		{ID: "one", Role: "user", Content: session.ClearMarkerText},
		{ID: "two", Role: "assistant", Content: session.ClearMarkerText},
		{ID: "three", Type: session.EntryTypeSystem, Role: "system", Content: session.ClearMarkerText + " (note)"},
	} {
		e.AgentID = "mia"
		e.Timestamp = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
		require.NoError(t, s.AppendTranscriptStrict(sid, e))
	}
	p := pagingRead(t, api, sid, "")
	require.Equal(t, []string{"one", "two", "three"}, pagingIDs(p), "only exact type+role+text marker cuts")
	require.Nil(t, p.PageInfo.LatestClear)
	pagingShape(t, p, false, false)
}

func TestChatHistoryPage_MergedProjectionParity(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	s := api.agentLoop.GetSessionStore()
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	call, err := s.AppendTranscriptAddressed(sid, session.TranscriptEntry{ID: "owner", Type: session.EntryTypeToolCall, AgentID: "mia", Timestamp: at,
		ToolCalls: []session.ToolCall{{ID: "call", Tool: "read_file", Status: "pending", Result: map[string]any{"text": "original"}}}})
	require.NoError(t, err)
	_, err = s.SettleToolCall(sid, call, "pending", session.ToolCall{ID: "call", Tool: "read_file", Status: "success", Result: map[string]any{"text": "actual"}})
	require.NoError(t, err)
	_, err = s.SettleToolCall(sid, call, "pending", session.ToolCall{ID: "call", Tool: "read_file", Status: "denied"})
	require.NoError(t, err)
	p1, err := s.ProjectToolCalls(sid, []session.ToolCallProjectionEdit{{Target: call, ToolCallID: "call", ContentState: "capped", Text: "short"}})
	require.NoError(t, err)
	p2, err := s.ProjectToolCalls(sid, []session.ToolCallProjectionEdit{{Target: call, ToolCallID: "call", ContentState: "emptied", Text: "gone"}})
	require.NoError(t, err)
	_, err = s.RetractToolCallEffects(sid, []session.ArchiveAddress{p2})
	require.NoError(t, err)
	for _, e := range []session.TranscriptEntry{
		{ID: "discarded", Role: "user", Content: "not delivered", ClientMessageID: "client-1"},
		{ID: "answer", Role: "assistant", Content: "partial", TurnID: "T2"},
		{ID: "cancel", Type: session.EntryTypeTurnCancelled, TurnID: "T2", CancelMethod: "graceful"},
	} {
		e.AgentID = "mia"
		e.Timestamp = at
		require.NoError(t, s.AppendTranscriptStrict(sid, e))
	}
	require.NoError(t, s.RecordInputDiscarded(sid, "discarded", "client-1"))
	canonical := pagingCanonicalBytes(t, s, sid)
	p := pagingRead(t, api, sid, "limit=20")
	byID := map[string]generated.Message{}
	for _, e := range p.Entries {
		byID[e.Id] = e
	}
	owner, ok := byID["owner"]
	require.True(t, ok, "tool-only owner survives without fake prose")
	require.NotNil(t, owner.ToolCalls)
	require.Len(t, *owner.ToolCalls, 1)
	tc := (*owner.ToolCalls)[0]
	require.Equal(t, "success", string(tc.Status), "mismatching second settle cannot overwrite success")
	require.NotNil(t, tc.ContentState, "P1 content_state is retained")
	require.Equal(t, "capped", string(*tc.ContentState), "retracted P2 reveals P1")
	require.NotNil(t, tc.Result, "P1 result is retained")
	require.Equal(t, map[string]any{"text": "short"}, *tc.Result, "independently derived P1 projection")
	d := byID["discarded"].InputDisposition
	require.NotNil(t, d)
	require.Equal(t, "discarded", string(d.State))
	require.Equal(t, "stopped_before_delivery", string(d.Reason))
	require.NotNil(t, byID["answer"].Truncated)
	require.True(t, *byID["answer"].Truncated)
	require.NotNil(t, byID["answer"].TruncationReason)
	require.Equal(t, "cancelled", string(*byID["answer"].TruncationReason))
	require.Equal(t, canonical, pagingCanonicalBytes(t, s, sid), "page projection never edits canonical bytes")
	_, err = s.RetractToolCallEffects(sid, []session.ArchiveAddress{p1})
	require.NoError(t, err)
	p = pagingRead(t, api, sid, "limit=20")
	for _, e := range p.Entries {
		if e.Id == "owner" {
			require.Equal(t, map[string]any{"text": "actual"}, *(*e.ToolCalls)[0].Result, "retract all restores settled original")
			return
		}
	}
	t.Fatal("owner disappeared after retracting a projection")
}

// D-V1b: a syntactically valid unknown selector is distinct from invalid syntax.
func TestChatHistoryPosition_RejectsPlausibleButUnknownToken(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	w := pagingRequest(api, sid, "before=hp1_"+strings.Repeat("A", 43))
	require.Equal(t, http.StatusNotFound, w.Code, "well-formed unknown selector is404, never latest fallback")
	var e generated.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
	require.Equal(t, "history position not found", e.Error)
}
