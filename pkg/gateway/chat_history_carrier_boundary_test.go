package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func pagingCarrierJSONDigest(t *testing.T, raw []byte) string {
	t.Helper()
	var value any
	require.NoError(t, json.Unmarshal(raw, &value))
	canonical, err := json.Marshal(value)
	require.NoError(t, err)
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}

func pagingCarrierBoundaryExpected(t *testing.T, sid string, tc pagingCarrierBoundaryCase, addresses []session.ArchiveAddress) ([]generated.Message, [][]byte) {
	t.Helper()
	messages := make([]generated.Message, 0, tc.want)
	frames := make([][]byte, 0, 2*tc.want+1)
	for i := pagingCarrierGroups - tc.want + 1; i <= pagingCarrierGroups; i++ {
		e := pagingCarrierBoundaryEntry(t, sid, tc.kind, i)
		position := pagingPositionOracle(sid, addresses[i-1])
		message, owned := pagingCarrierBoundaryReference(t, sid, e, position)
		messages = append(messages, message)
		frames = append(frames, owned...)
	}
	count := float64(len(frames))
	done, err := json.Marshal(generated.DoneFrame{Type: "done", SessionId: sid, Stats: &generated.DoneStats{FramesEmitted: &count}})
	require.NoError(t, err)
	frames = append(frames, done)
	return messages, frames
}

func pagingCarrierBoundaryPage(t *testing.T, sid string, tc pagingCarrierBoundaryCase, addresses []session.ArchiveAddress, page generated.SessionHistoryResponse, limit int) {
	t.Helper()
	messages, frames := pagingCarrierBoundaryExpected(t, sid, tc, addresses)
	require.Equal(t, messages, page.Entries, "independently frozen nearest source range; REST-only or WS-only selection must not survive")
	require.Equal(t, tc.want, page.PageInfo.RowCount)
	require.Equal(t, tc.want, page.PageInfo.SourceGroupCount)
	require.Equal(t, limit, page.PageInfo.Limit)
	require.Equal(t, 20, page.PageInfo.MinRows)
	require.Equal(t, "byte_budget", page.PageInfo.StopReason, "neither the row target, floor nor true end can choose this boundary")
	require.EqualValues(t, pagingCarrierBudget, page.PageInfo.SoftByteBudget)
	require.False(t, page.PageInfo.ByteBudgetExceeded)
	require.Nil(t, page.PageInfo.BudgetOverride)
	require.True(t, page.HasEarlier)
	require.False(t, page.HasLater)
	first := pagingPositionOracle(sid, addresses[pagingCarrierGroups-tc.want])
	last := pagingPositionOracle(sid, addresses[pagingCarrierGroups-1])
	require.Equal(t, &first, page.PageInfo.FirstPosition)
	require.Equal(t, &last, page.PageInfo.LastPosition)
	require.Equal(t, &first, page.NextBefore)
	require.Nil(t, page.NextAfter)
	rest, err := json.Marshal(messages)
	require.NoError(t, err)
	ws := 0
	for _, frame := range frames {
		ws += len(frame)
	}
	content := max(len(rest), ws)
	require.GreaterOrEqual(t, page.PageInfo.ProjectedBytes, int64(content), "actual larger independent carrier content is charged")
	require.Less(t, page.PageInfo.ProjectedBytes, int64(content+pagingCarrierEnvelope), "small frozen envelope/context charged once, not per group/frame")
	require.LessOrEqual(t, page.PageInfo.ProjectedBytes, int64(pagingCarrierBudget))
	t.Logf("I3 frozen carrier cuts REST=%d WS=%d; selected=%d; reference selected REST bytes=%d WS bytes=%d projected=%d", tc.restCut, tc.wsCut, tc.want, len(rest), ws, page.PageInfo.ProjectedBytes)
}

func pagingCarrierBoundarySnapshot(t *testing.T, h *historyD16Harness, sid string, tc pagingCarrierBoundaryCase, addresses []session.ArchiveAddress, page generated.SessionHistoryResponse) {
	t.Helper()
	peer := h.connect()
	limit, floor := 100, 20
	require.NoError(t, peer.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: sid, HistoryLimit: &limit, HistoryMinRows: &floor}))
	wire := h.readUntil(peer, func(raw []byte) bool { return historyD16Type(t, raw) == "catch_up_complete" })
	_, expected := pagingCarrierBoundaryExpected(t, sid, tc, addresses)
	actual := make([][]byte, 0, len(expected))
	snapshots, states, completed := 0, 0, 0
	for _, raw := range wire {
		switch historyD16Type(t, raw) {
		case "session_snapshot":
			snapshots++
			var snapshot generated.SessionSnapshotFrame
			require.NoError(t, json.Unmarshal(raw, &snapshot))
			require.Equal(t, page.PageInfo, snapshot.PageInfo, "same actual100/20 factory options/cut on REST and snapshot")
			require.Equal(t, page.NextBefore, snapshot.NextBefore)
			require.Equal(t, page.NextAfter, snapshot.NextAfter)
			require.Equal(t, page.HasEarlier, snapshot.HasEarlier)
			require.Equal(t, page.HasLater, snapshot.HasLater)
		case "session_state":
			states++
		case "catch_up_complete":
			completed++
			var completion generated.CatchUpCompleteFrame
			require.NoError(t, json.Unmarshal(raw, &completion))
			require.Equal(t, "snapshot", completion.Mode)
		default:
			actual = append(actual, raw) // exact historical content plus its one done
		}
	}
	require.Equal(t, 1, snapshots)
	require.Equal(t, 1, states)
	require.Equal(t, 1, completed)
	require.Len(t, actual, len(expected), "complete original groups, one historical batch end, no lookahead/orphan/extra live content")
	for i := range expected {
		require.Equal(t, pagingCarrierJSONDigest(t, expected[i]), pagingCarrierJSONDigest(t, actual[i]), "exact generated source frame%d, including independent original ID/position/payload", i)
	}
}

// T32/B46/FR-024/SC-014. Unlike the old20/20 divergence controls, these
// independently frozen100/20 datasets force the larger carrier to choose a
// boundary before the row target AND above the height floor, in both directions.
func TestHistoryPageProjection_ByteSelectionDistinguishesBothCarriers(t *testing.T) {
	for _, tc := range pagingCarrierBoundaryCases() {
		t.Run(tc.name, func(t *testing.T) {
			home, cfg := historyD16Config(t)
			provider := &historyD16Provider{}
			h := newHistoryD16Harness(t, home, cfg, provider, false)
			sid := h.session()
			addresses := pagingCarrierBoundarySeed(t, h, sid, tc)
			canonical := pagingCanonicalBytes(t, h.store, sid)
			readLogs := pagingLogCapture(t)
			page := h.page(sid, "?limit=100&min_rows=20")
			pagingCarrierBoundaryPage(t, sid, tc, addresses, page, 100)
			rows := pagingSummaryRows(readLogs())
			require.Len(t, rows, 1)
			require.EqualValues(t, tc.want, rows[0]["selected_archive_records"])
			require.LessOrEqual(t, pagingCounter(t, rows[0], "lookahead_archive_records"), float64(1))
			// Detail intentionally uses40/20. Both fixed byte cuts are below40;
			// compare like options via a separate real default REST request.
			defaults := h.page(sid, "")
			pagingCarrierBoundaryPage(t, sid, tc, addresses, defaults, 40)
			detail := h.detail(sid)
			require.Equal(t, "available", detail.HistoryState.Status)
			require.NotNil(t, detail.Messages)
			require.Equal(t, defaults.Entries, *detail.Messages)
			require.Equal(t, &defaults.PageInfo, detail.PageInfo)
			require.Equal(t, defaults.NextBefore, detail.NextBefore)
			pagingCarrierBoundarySnapshot(t, h, sid, tc, addresses, page)
			require.Equal(t, canonical, pagingCanonicalBytes(t, h.store, sid), "real carriers never rewrite canonical source or execute effects")
			require.Zero(t, provider.count(), "history paging invokes no model")
		})
	}
}
