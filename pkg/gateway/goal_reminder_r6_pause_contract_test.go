package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoalReminderR6PauseIsPublicThroughRESTAndLiveHub(t *testing.T) {
	f := r6NewPublicFixture(t)
	prior, err := f.al.GetSessionLifecycleStore().Load(f.root)
	require.NoError(t, err)
	f.provider.mu.Lock()
	f.provider.hold = true
	f.provider.mu.Unlock()
	f.age(t)
	f.engine.Tick(context.Background())
	var gate chan struct{}
	select {
	case gate = <-f.provider.requests:
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP: actual public keeper/notifier/ordinary receiver did not reach its real provider")
	}
	current, err := f.al.GetSessionLifecycleStore().Load(f.root)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleRunning, current.State)
	require.NotNil(t, current.ExecutionID)
	require.NotEqual(t, prior.ExecutionID.RunID, current.ExecutionID.RunID)
	require.Equal(t, prior.Generation+1, current.Generation)
	// Boundary isolation complements the agent pack's full five-outcome replay.
	// Four prior assessed outcomes are durable; THIS actually executing fifth
	// reminder must finish before any pause decision can be made.
	_, err = goal.NewStore(config.OmnipusHomeDir()).Update(f.goalID, func(g *goal.Goal) error { g.ZeroOutputPushes = 4; return nil })
	require.NoError(t, err)
	beforePause := r6ReplayFrames(t, f.frames)
	require.NotEmpty(t, beforePause, "positive instrument: real admitted reminder reaches the real hub/send queue")
	for _, frame := range beforePause {
		assert.NotEqual(t, r6GatewayPause, frame.Content, "a still-running fifth outcome cannot pause")
	}
	beforeDispatch := f.count()
	require.Equal(t, 1, beforeDispatch, "one real keeper notification has actually been issued")
	close(gate)
	r6WarnAwaitProducingPause(t, f, beforeDispatch)
	assert.Equal(t, beforeDispatch, f.count(), "fifth completed unproductive outcome must publish no sixth notification")
	w := httptest.NewRecorder()
	f.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+f.root, nil))
	require.Equal(t, http.StatusOK, w.Code, "actual session REST view must remain readable")
	var detail generated.SessionDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	var rows []map[string]json.RawMessage
	for _, row := range r6PauseHistoryMessages(t, f, detail, w.Body.Bytes()) {
		if row.Content != nil && *row.Content == r6GatewayPause {
			rows = append(rows, r6MessageFields(t, row))
		}
	}
	require.Len(t, rows, 1, "R6: exact pause must be one PUBLIC REST system row, not a hidden bookmark/user bubble")
	row := rows[0]
	assert.JSONEq(t, `"system"`, string(row["role"]))
	assert.JSONEq(t, `"system"`, string(row["type"]))
	assert.JSONEq(t, `"`+f.goalID+`"`, string(row["goal_id"]))
	var live []generated.ReplayMessageFrame
	for _, frame := range r6ReplayFrames(t, f.frames) {
		if frame.Content == r6GatewayPause {
			live = append(live, frame)
		}
	}
	require.Len(t, live, 1, "R6: the same pause must arrive LIVE through the real event bus/hub")
	assert.Equal(t, "replay_message", live[0].Type)
	assert.Equal(t, "system", live[0].Role)
	assert.Equal(t, f.root, live[0].SessionId)
	require.NotNil(t, live[0].Id)
	require.NotNil(t, live[0].GoalId)
	require.NotNil(t, live[0].Timestamp)
	assert.JSONEq(t, `"`+*live[0].Id+`"`, string(row["id"]), "REST/live use the same persisted ID")
	assert.JSONEq(t, `"`+*live[0].Timestamp+`"`, string(row["timestamp"]), "REST/live use the same persisted instant")
	assert.Equal(t, f.goalID, *live[0].GoalId)
	for _, other := range []chan []byte{f.other, f.unbound} {
		for _, frame := range r6ReplayFrames(t, other) {
			assert.NotEqual(t, r6GatewayPause, frame.Content, "the original session's pause must not leak to unrelated/unbound tabs")
		}
	}
	persisted, err := goal.NewStore(config.OmnipusHomeDir()).Get(f.goalID)
	require.NoError(t, err)
	assert.Equal(t, 5, persisted.ZeroOutputPushes)
	assert.Equal(t, "active", string(persisted.State))
	assert.Equal(t, 0, persisted.Round, "idle pause is not a timer adjudication")
}

func r6MessageFields(t *testing.T, message any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(message)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	return fields
}

// r6PauseHistoryMessages preserves the original whole-history duplicate oracle.
// Only canonical REST pages are read; no raw/lifetime store fallback is used.
func r6PauseHistoryMessages(t *testing.T, f *r6PublicFixture, detail generated.SessionDetail, rawDetail []byte) []generated.Message {
	t.Helper()
	validationMessage, schemaError := validateBodyAgainstSchema("SessionDetail", rawDetail)
	require.False(t, schemaError, "Pause history detail schema must be available.")
	require.True(t, validationMessage == "", "Pause history detail violates its generated contract.")
	require.Equal(t, "available", detail.HistoryState.Status, "Pause history must be available, never silently empty.")
	require.True(t, detail.HistoryState.Reason == nil && detail.HistoryState.Message == nil, "Available pause history must omit unavailable-only fields.")
	require.NotNil(t, detail.Messages, "Available pause history must carry messages.")
	require.NotNil(t, detail.PageInfo, "Available pause history must carry page metadata.")
	require.NotNil(t, detail.HasEarlier, "Available pause history must carry its earlier flag.")
	require.NotNil(t, detail.HasLater, "Available pause history must carry its later flag.")
	require.False(t, *detail.HasLater, "Default detail is the latest retained page.")
	page := generated.SessionHistoryResponse{
		SessionId: detail.Session.Id, Entries: *detail.Messages, PageInfo: *detail.PageInfo,
		HasEarlier: *detail.HasEarlier, HasLater: *detail.HasLater,
		NextBefore: detail.NextBefore, NextAfter: detail.NextAfter,
	}
	generation := page.PageInfo.RetentionGeneration
	seenPositions, seenCursors := map[string]bool{}, map[string]bool{}
	var messages []generated.Message
	mode := "latest"
	for {
		require.True(t, page.SessionId == f.root, "Pause history must retain its addressed session identity.")
		require.Equal(t, mode, page.PageInfo.Mode, "Pause history must honor the actual selected page direction.")
		require.True(t, page.PageInfo.RetentionGeneration == generation, "Retained pause history changed while counting its notices.")
		require.NotNil(t, page.Entries, "Available pause history cannot substitute null entries.")
		require.Equal(t, len(page.Entries), page.PageInfo.SourceGroupCount, "Pause history source-group count must match its entries.")
		require.Equal(t, page.HasEarlier, page.NextBefore != nil, "Pause history earlier cursor is required iff earlier groups exist.")
		require.Equal(t, page.HasLater, page.NextAfter != nil, "Pause history later cursor is required iff later groups exist.")
		require.LessOrEqual(t, page.PageInfo.MinRows, page.PageInfo.Limit, "Pause history row floor cannot exceed its target.")
		require.Equal(t, page.PageInfo.ProjectedBytes > page.PageInfo.SoftByteBudget, page.PageInfo.ByteBudgetExceeded, "Pause history budget flag must match its measured bytes.")
		require.Equal(t, page.PageInfo.ByteBudgetExceeded, page.PageInfo.BudgetOverride != nil, "Pause history budget override must match its flag.")
		if page.PageInfo.StopReason == "byte_budget" {
			require.GreaterOrEqual(t, page.PageInfo.RowCount, page.PageInfo.MinRows, "A byte-budget stop cannot underfill the history row floor.")
		}
		if len(page.Entries) == 0 {
			require.False(t, page.HasEarlier, "Empty history cannot have an earlier continuation.")
			require.False(t, page.HasLater, "Empty history cannot have a later continuation.")
			require.Nil(t, page.PageInfo.FirstPosition, "Empty history has no first occurrence.")
			require.Nil(t, page.PageInfo.LastPosition, "Empty history has no last occurrence.")
		} else {
			require.NotNil(t, page.PageInfo.FirstPosition, "Nonempty history must have a first occurrence.")
			require.NotNil(t, page.PageInfo.LastPosition, "Nonempty history must have a last occurrence.")
			for _, entry := range page.Entries {
				require.NotNil(t, entry.HistoryPosition, "Every paged history occurrence must have its canonical position.")
				require.False(t, seenPositions[*entry.HistoryPosition], "Pause history pages must not repeat an occurrence.")
				seenPositions[*entry.HistoryPosition] = true
			}
			require.True(t, *page.PageInfo.FirstPosition == *page.Entries[0].HistoryPosition, "Pause history first boundary must match its actual occurrence.")
			require.True(t, *page.PageInfo.LastPosition == *page.Entries[len(page.Entries)-1].HistoryPosition, "Pause history last boundary must match its actual occurrence.")
			if page.HasEarlier {
				require.True(t, *page.NextBefore == *page.PageInfo.FirstPosition, "Pause history earlier cursor must match its selected boundary.")
			}
			if page.HasLater {
				require.True(t, *page.NextAfter == *page.PageInfo.LastPosition, "Pause history later cursor must match its selected boundary.")
			}
		}
		// Prepend whole pages, preserving accepted order and duplicate message IDs.
		messages = append(page.Entries, messages...)
		if !page.HasEarlier {
			return messages
		}
		require.NotEmpty(t, page.Entries, "History continuation must make actual progress.")
		cursor := *page.NextBefore
		require.NotEmpty(t, cursor, "History continuation requires its actual cursor.")
		require.False(t, seenCursors[cursor], "Pause history continuation cannot cycle.")
		seenCursors[cursor] = true
		w := httptest.NewRecorder()
		f.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+url.PathEscape(f.root)+"/messages?before="+url.QueryEscape(cursor), nil))
		require.Equal(t, http.StatusOK, w.Code, "Pause history continuation must remain readable through the existing REST route.")
		validationMessage, schemaError = validateBodyAgainstSchema("SessionHistoryResponse", w.Body.Bytes())
		require.False(t, schemaError, "Pause history page schema must be available.")
		require.True(t, validationMessage == "", "Pause history page violates its generated contract.")
		var older generated.SessionHistoryResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &older), "Pause history page must decode as its generated carrier.")
		page, mode = older, "older"
	}
}

// TestR6PauseHistoryMessagesRetainsOlderPageDuplicate makes the whole-history
// count oracle depend on a real older page, not just the default latest tail.
func TestR6PauseHistoryMessagesRetainsOlderPageDuplicate(t *testing.T) {
	f := r6NewPublicFixture(t)
	store := f.al.GetSessionStore()
	meta, err := store.GetMeta(f.root)
	require.NoError(t, err, "BLOCKED: real session ownership is required for the paging premise.")
	require.NotEmpty(t, meta.AgentID)
	// A page holds at most 100 source groups; these 102 distinct whole entries
	// force a boundary independently of logical-row weighting or byte budgets.
	const seedCount = 102
	expectedIDs := make(map[string]bool, seedCount)
	var pauseIDs []string
	at := time.Now().UTC().Add(-time.Second)
	for i := 0; i < seedCount; i++ {
		entry := session.TranscriptEntry{
			ID: "r6-paged-seed-" + time.Duration(i).String(), Type: session.EntryTypeMessage,
			Role: "user", AgentID: meta.AgentID, Content: "Benign whole-history paging filler.",
			Timestamp: at.Add(time.Duration(i) * time.Millisecond),
		}
		if i == 0 || i == seedCount-1 {
			entry.Type, entry.Role, entry.Content, entry.GoalID = session.EntryTypeSystem, "system", r6GatewayPause, f.goalID
			pauseIDs = append(pauseIDs, entry.ID)
		}
		expectedIDs[entry.ID] = true
		require.NoError(t, store.AppendTranscriptStrict(f.root, entry), "BLOCKED: seed must use the real canonical append/index pipeline.")
	}
	require.Equal(t, seedCount, len(expectedIDs))
	require.Equal(t, 2, len(pauseIDs))
	require.True(t, pauseIDs[0] != pauseIDs[1], "The two saved pause notices must have distinct identities.")
	w := httptest.NewRecorder()
	f.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+url.PathEscape(f.root), nil))
	require.Equal(t, http.StatusOK, w.Code, "BLOCKED: actual authorized latest history must be readable.")
	validationMessage, schemaError := validateBodyAgainstSchema("SessionDetail", w.Body.Bytes())
	require.False(t, schemaError, "BLOCKED: actual detail schema must be available.")
	require.True(t, validationMessage == "", "BLOCKED: actual detail must satisfy its contract.")
	var detail generated.SessionDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.Equal(t, "available", detail.HistoryState.Status, "BLOCKED: unavailable history is not an empty successful premise.")
	require.NotNil(t, detail.Messages)
	require.NotNil(t, detail.PageInfo)
	require.NotNil(t, detail.HasEarlier)
	require.NotNil(t, detail.HasLater)
	require.True(t, *detail.HasEarlier, "BLOCKED: latest carrier must prove a real older-page boundary.")
	require.False(t, *detail.HasLater)
	require.NotNil(t, detail.NextBefore)
	require.Equal(t, "latest", detail.PageInfo.Mode)
	require.Equal(t, 40, detail.PageInfo.Limit, "SessionDetail uses the contract's unmodified default row target.")
	require.Equal(t, 20, detail.PageInfo.MinRows, "SessionDetail uses the contract's unmodified default floor.")
	latestPauseCount, olderInLatest, recentInLatest := 0, false, false
	for _, row := range *detail.Messages {
		olderInLatest = olderInLatest || row.Id == pauseIDs[0]
		recentInLatest = recentInLatest || row.Id == pauseIDs[1]
		if row.Content != nil && *row.Content == r6GatewayPause {
			latestPauseCount++
		}
	}
	require.False(t, olderInLatest, "BLOCKED: the older seed notice must be outside the initial page.")
	require.True(t, recentInLatest, "BLOCKED: the recent seed notice must be inside the initial page.")
	require.Equal(t, 1, latestPauseCount)
	// Independently follow the actual generated cursor chain to establish the
	// physical multipage premise before invoking the reader under test.
	cursor, pageCount, olderPauseCount := detail.NextBefore, 1, 0
	seenCursors := map[string]bool{}
	for cursor != nil {
		require.False(t, seenCursors[*cursor], "BLOCKED: the independent history premise must not cycle.")
		seenCursors[*cursor] = true
		olderW := httptest.NewRecorder()
		f.api.HandleSessions(olderW, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+url.PathEscape(f.root)+"/messages?before="+url.QueryEscape(*cursor), nil))
		require.Equal(t, http.StatusOK, olderW.Code, "BLOCKED: real older history must remain readable.")
		validationMessage, schemaError = validateBodyAgainstSchema("SessionHistoryResponse", olderW.Body.Bytes())
		require.False(t, schemaError, "BLOCKED: actual older schema must be available.")
		require.True(t, validationMessage == "", "BLOCKED: actual older carrier must satisfy its contract.")
		var older generated.SessionHistoryResponse
		require.NoError(t, json.Unmarshal(olderW.Body.Bytes(), &older))
		require.True(t, older.SessionId == f.root, "BLOCKED: older pages must retain addressed session identity.")
		require.Equal(t, "older", older.PageInfo.Mode)
		require.NotEmpty(t, older.Entries, "BLOCKED: the independent older-page chain must make progress.")
		require.Equal(t, older.HasEarlier, older.NextBefore != nil)
		pageCount++
		for _, row := range older.Entries {
			if row.Id == pauseIDs[0] {
				require.NotNil(t, row.Content)
				require.True(t, *row.Content == r6GatewayPause)
				olderPauseCount++
			}
		}
		cursor = older.NextBefore
	}
	require.GreaterOrEqual(t, pageCount, 2)
	require.Equal(t, 1, olderPauseCount, "The specific older saved notice must be witnessed through the actual older carrier chain.")
	t.Logf("R6_MULTIPAGE_PREMISE pages=%d latest_entries=%d older_pause=%d latest_pause=%d seeded_entries=%d", pageCount, len(*detail.Messages), olderPauseCount, latestPauseCount, seedCount)
	all := r6PauseHistoryMessages(t, f, detail, w.Body.Bytes())
	seenIDs, pauseCount := map[string]int{}, 0
	for _, row := range all {
		if expectedIDs[row.Id] {
			seenIDs[row.Id]++
		}
		if row.Content != nil && *row.Content == r6GatewayPause {
			pauseCount++
		}
	}
	require.Equal(t, 2, pauseCount, "The whole-history reader must retain both distinct saved pause notices, including the older-page duplicate.")
	for _, id := range pauseIDs {
		require.Equal(t, 1, seenIDs[id], "Each specifically arranged pause identity must survive exactly once.")
	}
	require.Equal(t, seedCount, len(seenIDs), "The whole-history reader must retain the full independently arranged seed set.")
	for id := range expectedIDs {
		require.Equal(t, 1, seenIDs[id], "Every independently arranged source entry must survive exactly once.")
	}
	t.Logf("R6_MULTIPAGE_RESULT retained_seed_entries=%d retained_pause_entries=%d", len(seenIDs), pauseCount)
}
