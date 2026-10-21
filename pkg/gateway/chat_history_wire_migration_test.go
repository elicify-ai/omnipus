package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// Spec D10/D15: these helpers adapt assertions, never replace the real handler
// or re-serialize a guessed response. Metadata expectations come from the small
// real fixtures (all below40 groups) and the declared40/20 defaults.
func historyMigrationMetadata(t *testing.T, info generated.SessionHistoryPageInfo, mode string) {
	t.Helper()
	require.Equal(t, mode, info.Mode)
	require.Equal(t, 40, info.Limit)
	require.Equal(t, 20, info.MinRows)
	require.EqualValues(t, 4194304, info.SoftByteBudget)
	require.NotEmpty(t, info.RetentionGeneration)
	require.NotNil(t, info.Context.Spans)
	require.NotNil(t, info.Context.DuplicateIds)
}

func historyMigrationPage(t *testing.T, sid string, body []byte, mode string, earlier, later bool) generated.SessionHistoryResponse {
	t.Helper()
	var page generated.SessionHistoryResponse
	require.NoError(t, json.Unmarshal(body, &page), "D10 generated history envelope, never the retired bare array")
	require.Equal(t, sid, page.SessionId)
	pagingShape(t, page, earlier, later)
	historyMigrationMetadata(t, page.PageInfo, mode)
	message, serverError := validateBodyAgainstSchema("SessionHistoryResponse", body)
	require.False(t, serverError, "schema instrument must compile")
	require.Empty(t, message, "actual generated envelope must conform, not a hand-written carrier")
	return page
}

func historyMigrationDetail(t *testing.T, sid string, body []byte, earlier bool) generated.SessionDetail {
	t.Helper()
	var detail generated.SessionDetail
	require.NoError(t, json.Unmarshal(body, &detail), "D10 generated detail union")
	require.Equal(t, sid, detail.Session.Id)
	require.Equal(t, "available", detail.HistoryState.Status, "a successful metadata read alone is not loaded history")
	require.Nil(t, detail.HistoryState.Reason)
	require.Nil(t, detail.HistoryState.Message)
	require.NotNil(t, detail.Messages)
	require.NotNil(t, *detail.Messages, "available empty history is[], not null")
	require.NotNil(t, detail.HasEarlier)
	require.NotNil(t, detail.HasLater)
	require.NotNil(t, detail.PageInfo)
	// The assertion adapter uses only actual generated fields from this body.
	// It does not synthesize absent union fields or implement a page reader.
	page := generated.SessionHistoryResponse{
		SessionId: detail.Session.Id, Entries: *detail.Messages,
		HasEarlier: *detail.HasEarlier, HasLater: *detail.HasLater,
		NextBefore: detail.NextBefore, NextAfter: detail.NextAfter, PageInfo: *detail.PageInfo,
	}
	pagingShape(t, page, earlier, false)
	historyMigrationMetadata(t, *detail.PageInfo, "latest")
	message, serverError := validateBodyAgainstSchema("SessionDetail", body)
	require.False(t, serverError)
	require.Empty(t, message)
	return detail
}

func historyMigrationSnapshot(t *testing.T, sid string, raw []byte, earlier bool, groups, rows int) generated.SessionSnapshotFrame {
	t.Helper()
	var frame generated.SessionSnapshotFrame
	require.NoError(t, json.Unmarshal(raw, &frame), "D10 generated snapshot carrier")
	require.Equal(t, "session_snapshot", frame.Type)
	require.Equal(t, sid, frame.SessionId)
	require.Equal(t, earlier, frame.HasEarlier)
	require.False(t, frame.HasLater)
	require.Nil(t, frame.NextAfter)
	historyMigrationMetadata(t, frame.PageInfo, "latest")
	require.Equal(t, groups, frame.PageInfo.SourceGroupCount)
	require.Equal(t, rows, frame.PageInfo.RowCount)
	if groups > 0 {
		require.NotNil(t, frame.PageInfo.FirstPosition)
		require.NotNil(t, frame.PageInfo.LastPosition)
		require.Regexp(t, `^hp1_[A-Za-z0-9_-]{43}$`, *frame.PageInfo.FirstPosition)
		require.Regexp(t, `^hp1_[A-Za-z0-9_-]{43}$`, *frame.PageInfo.LastPosition)
	}
	if earlier {
		require.Equal(t, frame.PageInfo.FirstPosition, frame.NextBefore)
	} else {
		require.Nil(t, frame.NextBefore)
	}
	return frame
}

// not-wire-format: test-only extraction of actual nested bytes for the existing
// per-message schema/field-omission assertions, after generated carrier checks.
func historyMigrationRawEntries(t *testing.T, body []byte, field string) []json.RawMessage {
	t.Helper()
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &document))
	raw, ok := document[field]
	require.True(t, ok, "actual carrier must contain %s", field)
	var entries []json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &entries))
	require.NotNil(t, entries)
	return entries
}

func historyMigrationEntryObjects(t *testing.T, body []byte, field string) []map[string]any {
	t.Helper()
	raw := historyMigrationRawEntries(t, body, field)
	entries := make([]map[string]any, 0, len(raw))
	for _, data := range raw {
		var entry map[string]any
		require.NoError(t, json.Unmarshal(data, &entry))
		require.NotNil(t, entry)
		entries = append(entries, entry)
	}
	return entries
}
