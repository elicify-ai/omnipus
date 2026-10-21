package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Section M's event/hook/sentinel are backend-owned type-level assumptions,
// consumed from declaration commit688a866136c003ff41ec5be5aeb87cdd7b04f2ff.
// The nil declaration is not implementation: real placement, validators and
// endpoint classification remain GREEN deliverables. No hook returns an error.
func pagingPreparedFaultDiagnostic(t *testing.T, point, outcome string, wantError error) {
	t.Helper()
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	const body = "PRIVATE_REAL_POST_READ_FAULT_BODY_1266"
	const entryID = "PRIVATE_REAL_POST_READ_FAULT_ENTRY_1266"
	const agentID = "PRIVATE_REAL_POST_READ_FAULT_AGENT_1266"
	const query = "private_mark=PRIVATE_POST_READ_QUERY_CANARY_1266"
	address, err := store.AppendTranscriptAddressed(sid, session.TranscriptEntry{
		ID: entryID, Role: "user", Content: body, AgentID: agentID,
		Timestamp:      time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
		ViewMembership: session.ViewMembershipChat,
	})
	require.NoError(t, err)
	canaries := []string{sid, body, entryID, agentID, query, "PRIVATE_POST_READ_QUERY_CANARY_1266", store.BaseDir(), api.homePath, pagingPositionOracle(sid, address), fmt.Sprintf("%+v", address), session.ErrHistoryProjection.Error(), errHistoryPageProtocol.Error()}
	read := pagingLogCapture(t) // Actual installed bridge/file sink has its own positive control.
	previous := historyPageFaultHook
	require.Nil(t, previous, "serial same-package hook has no leaked owner")
	t.Cleanup(func() { historyPageFaultHook = previous })

	// No universal reader/index failure can pass a later negative test. This
	// same source, real endpoint and generated envelope must work without fault.
	before := len(pagingSummaryRows(read()))
	baseline := pagingRead(t, api, sid, query)
	require.Equal(t, []string{entryID}, pagingIDs(baseline))
	pagingShape(t, baseline, false, false)
	require.NotNil(t, baseline.Entries[0].Content)
	require.Equal(t, body, *baseline.Entries[0].Content)
	rows := pagingSummaryRows(read())
	require.Len(t, rows, before+1, "control: exactly one successful real history_page")
	pagingAssertSummary(t, rows[before], "success", "latest", true, true, canaries)
	require.EqualValues(t, 1, rows[before]["selected_archive_records"])
	require.Greater(t, pagingCounter(t, rows[before], "selected_archive_bytes"), float64(0))

	var reached atomic.Int32
	historyPageFaultHook = func(event historyPageFaultEvent) {
		if event.SessionID != sid || event.Point != point {
			return
		}
		reached.Add(1)
		switch point {
		case "after_weigh":
			require.NotNil(t, event.Weight, "mutate actual gateway-prepared weight")
			require.Nil(t, event.Prepared)
			event.Weight.ProjectedBytes = -1 // Only the decided fault; real strict weight validation must reject it.
		case "before_carrier_validate":
			require.NotNil(t, event.Prepared, "mutate actual selected generated carrier")
			require.Nil(t, event.Weight)
			event.Prepared.Response.HasEarlier = true
			event.Prepared.Response.NextBefore = nil // Contradiction, not caller syntax and not a canned error.
		default:
			t.Fatalf("test selected an undecided fault point %q", point)
		}
	}

	// Check the real error chain as well as the endpoint's sanitized refusal;
	// the hook cannot manufacture either sentinel because it returns no error.
	request, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{}, historyPageProjectionDeps{ToolStore: newToolResultStore(api.homePath)})
	require.NoError(t, err, "actual shared request factory")
	_, err = request.Read(context.Background(), store)
	require.ErrorIs(t, err, wantError, "genuine produced-value validator/classifier, never generic not-implemented")
	require.NotErrorIs(t, err, session.ErrChatHistoryNotImplemented)
	require.EqualValues(t, 1, reached.Load(), "direct real pipeline reached the intended fault exactly once")

	before = len(pagingSummaryRows(read()))
	response := pagingRequest(api, sid, query)
	require.EqualValues(t, 2, reached.Load(), "real REST attempt also reached the same prepared-value hook exactly once")
	require.Equal(t, http.StatusInternalServerError, response.Code, "never success/partial content/caller400")
	var failure generated.ErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
	require.Equal(t, generated.ErrorResponse{Error: "This session could not be read because session storage failed. Check disk space and permissions, then retry. Details are in the server log."}, failure, "D11 retains C6's fixed read failure copy")
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	require.Equal(t, []string{"error"}, keys, "no partial entries, positions, continuation or success page metadata")
	for _, canary := range canaries {
		require.NotContains(t, response.Body.String(), canary, "sanitized error must not expose source/private/cause data")
	}
	rows = pagingSummaryRows(read())
	require.Len(t, rows, before+1, "exactly one failed summary, not an earlier success plus failure")
	pagingAssertSummary(t, rows[before], outcome, "latest", false, true, canaries)
	require.GreaterOrEqual(t, pagingCounter(t, rows[before], "selected_archive_records"), float64(1), "post-read fault charges actual selected work, not invented zero")
	require.Greater(t, pagingCounter(t, rows[before], "selected_archive_bytes"), float64(0))
}

func TestHistoryPageDiagnostics_RealWeightFaultIsProjectionError(t *testing.T) {
	pagingPreparedFaultDiagnostic(t, "after_weigh", "projection_error", session.ErrHistoryProjection)
}

func TestHistoryPageDiagnostics_RealCarrierFaultIsProtocol(t *testing.T) {
	pagingPreparedFaultDiagnostic(t, "before_carrier_validate", "protocol", errHistoryPageProtocol)
}
