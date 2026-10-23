package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func r1CleanupHealthy(t *testing.T, f r1CleanupFixture, want []string) {
	t.Helper()
	w := pagingRequest(f.api, f.sid, "")
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.Bytes())
	page := historyMigrationPage(t, f.sid, w.Body.Bytes(), "latest", false, false)
	require.Equal(t, want, pagingIDs(page))
}

func r1CleanupSummaryPrivacy(t *testing.T, row map[string]any, canaries []string) {
	t.Helper()
	for _, key := range []string{"session_id", "agent_id", "user_id", "query", "raw_query", "position", "history_position", "address", "partition_key", "byte_offset", "path", "error", "cause"} {
		assert.NotContains(t, row, key, "R1-1 fixed content-free summary must not acquire a private cause/identity field")
	}
	body, err := json.Marshal(row)
	require.NoError(t, err)
	for _, canary := range canaries {
		assert.NotContains(t, string(body), canary)
	}
	for _, key := range []string{"source_group_count", "row_count", "projected_bytes", "has_earlier", "has_later", "byte_budget_exceeded", "stop_reason", "retention_generation"} {
		assert.NotContains(t, row, key, "failed read never claims a successful empty page")
	}
}

// D11 + pin X + R1-1: cleanup storage failure outranks the read's abort/404
// classification while BOTH real causes remain internally available. No body
// delivery promise is imposed on a canceled transport. Pure errors stay pure.
func TestHistoryPageCleanupFailure_OutranksCancellationAndNotFound(t *testing.T) {
	driver := historyMeasurementDriver(t)
	t.Run("pure_read_errors_unchanged", func(t *testing.T) { r1CleanupPureControls(t, driver) })
	for _, boundary := range []string{"prepared_core", "rest_handler"} {
		for _, kind := range []string{"cancellation", "unknown_position"} {
			t.Run(boundary+"_"+kind, func(t *testing.T) { r1CleanupCombined(t, driver, boundary, kind) })
		}
	}
}

func r1CleanupPureControls(t *testing.T, driver string) {
	t.Helper()
	f := r1CleanupBuildFixture(t, driver)
	readLogs := pagingLogCapture(t)
	r1CleanupHealthy(t, f, []string{"r1-old", "r1-current"})
	for _, kind := range []string{"cancellation", "unknown_position"} {
		t.Run(kind, func(t *testing.T) {
			before := len(pagingSummaryRows(readLogs()))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancellation" {
				cancel()
			}
			query := r1CleanupQuery(kind)
			r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+f.sid+"/messages?"+query, nil).WithContext(ctx)
			w := httptest.NewRecorder()
			f.api.HandleSessions(w, r)
			rows := pagingSummaryRows(readLogs())
			require.Len(t, rows, before+1)
			outcome, mode := "abort", "latest"
			if kind == "unknown_position" {
				outcome, mode = "invalid_request", "older"
				require.Equal(t, http.StatusNotFound, w.Code)
				require.Equal(t, map[string]any{"error": "history position not found"}, decodeObject(t, w.Body.Bytes()))
			} else {
				require.Empty(t, w.Body.Bytes(), "genuine cancellation without cleanup fault has no fabricated response")
			}
			pagingAssertSummary(t, rows[before], outcome, mode, false, true, r1CleanupCanaries(f, "hp1_"+strings.Repeat("A", 43)))
		})
	}
}

func r1CleanupCombined(t *testing.T, driver, boundary, kind string) {
	t.Helper()
	f := r1CleanupBuildFixture(t, driver)
	readLogs := pagingLogCapture(t)
	r1CleanupHealthy(t, f, []string{"r1-old", "r1-current"})
	before := len(pagingSummaryRows(readLogs()))
	query := r1CleanupQuery(kind)
	w := httptest.NewRecorder()
	work := r1CleanupStart(t, func(ctx context.Context) error {
		if boundary == "prepared_core" {
			opts, _, err := parseChatHistoryQuery(query)
			if err != nil {
				return err
			}
			request, err := newHistoryPageRequest(f.sid, opts, f.api.historyProjectionDeps(f.store, f.sid))
			if err != nil {
				return err
			}
			_, err = request.Read(ctx, f.store)
			return err
		}
		ctx = context.WithValue(ctx, UserContextKey{}, &config.UserConfig{Username: "R1_USER_CANARY"})
		r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+f.sid+"/messages?"+query, nil).WithContext(ctx)
		f.api.HandleSessions(w, r)
		return nil // HTTP handler returns no error value; assertions inspect its REAL response and summary.
	})
	r1CleanupRetirePinned(t, f)
	clear, nativeError := r1CleanupHoldRemoveFault(t, f.retired)
	if kind == "cancellation" {
		work.cancel() // Actual cancellation occurs ONLY after real pin/retirement/native denial proof.
	}
	err := r1CleanupJoin(t, work)
	require.FileExists(t, f.retired, "real failed final release keeps pending file, never drops cleanup state")
	if boundary == "prepared_core" {
		require.ErrorIs(t, err, session.ErrChatHistoryUnavailable)
		var unavailable *session.ChatHistoryUnavailableError
		require.ErrorAs(t, err, &unavailable)
		require.Equal(t, "storage_error", unavailable.Reason)
		require.ErrorIs(t, err, nativeError, "actual native removal cause is retained internally")
		original := error(session.ErrHistoryPositionNotFound)
		if kind == "cancellation" {
			original = context.Canceled
		}
		require.True(t, errors.Is(err, original), "superseded read cause must not be silently lost")
		require.Equal(t, session.ErrChatHistoryUnavailable.Error(), err.Error(), "typed public error string is sanitized")
	} else {
		rows := pagingSummaryRows(readLogs())
		require.Len(t, rows, before+1, "exactly one actual REST history_page summary, never a new synthetic logger")
		mode := "latest"
		if kind == "unknown_position" {
			mode = "older"
			assert.Equal(t, http.StatusInternalServerError, w.Code, "R1-1 cleanup failure must not become a misleading404")
			assert.Equal(t, r1CleanupExpectedStorageBody(), decodeObject(t, w.Body.Bytes()))
		}
		// Classification failure must not prevent the independent privacy and
		// maintenance assertions below from executing on this RED implementation.
		t.Run("storage_classification", func(t *testing.T) {
			pagingAssertSummary(t, rows[before], "storage", mode, false, true, r1CleanupCanaries(f, "hp1_"+strings.Repeat("A", 43)))
		})
		r1CleanupSummaryPrivacy(t, rows[before], r1CleanupCanaries(f, "hp1_"+strings.Repeat("A", 43)))
	}
	clear()
	r1CleanupHealthy(t, f, []string{"r1-current"})
	_, statErr := os.Stat(f.retired)
	require.ErrorIs(t, statErr, os.ErrNotExist, "normal fresh REST read retries pending cleanup without new expiry")
}
