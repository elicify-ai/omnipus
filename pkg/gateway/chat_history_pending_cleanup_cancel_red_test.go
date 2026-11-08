package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B69/D11/D14: a NEW normal read is already cancelled while an earlier real
// failed retirement is pending. This is distinct from cancelling the original
// pinned reader during its final release (the existing R1-1 coverage).
func TestHistoryPageCleanupPendingFailure_PreCancelledRead(t *testing.T) {
	driver := historyMeasurementDriver(t)
	t.Run("cleanup_free_controls", func(t *testing.T) { r1CleanupPureControls(t, driver) })
	for _, boundary := range []string{"prepared_core", "rest_handler"} {
		t.Run(boundary, func(t *testing.T) {
			f := r1CleanupBuildFixture(t, driver)
			r1CleanupHealthy(t, f, []string{"r1-old", "r1-current"})
			work := r1CleanupStart(t, func(ctx context.Context) error {
				request, err := newHistoryPageRequest(f.sid, session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{})
				if err != nil {
					return err
				}
				_, err = request.Read(ctx, f.store)
				return err
			})
			r1CleanupRetirePinned(t, f)
			restore, native := r1CleanupHoldRemoveFault(t, f.retired)
			defer restore()
			established := r1CleanupJoin(t, work)
			require.ErrorIs(t, established, session.ErrChatHistoryUnavailable)
			require.ErrorIs(t, established, native, "instrument: actual failed last-reader release established pending cleanup")
			require.FileExists(t, f.retired)

			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			require.ErrorIs(t, cancelled.Err(), context.Canceled, "instrument: the new request is cancelled BEFORE it enters the real reader")
			if boundary == "prepared_core" {
				request, err := newHistoryPageRequest(f.sid, session.ChatHistoryPageOptions{Limit: 20}, f.api.historyProjectionDeps(f.store, f.sid))
				require.NoError(t, err)
				_, err = request.Read(cancelled, f.store)
				require.ErrorIs(t, err, session.ErrChatHistoryUnavailable, "B69: pending cleanup storage failure outranks this new request's pre-existing cancellation")
				var unavailable *session.ChatHistoryUnavailableError
				require.ErrorAs(t, err, &unavailable)
				require.Equal(t, "storage_error", unavailable.Reason)
				require.ErrorIs(t, err, native, "the actual native retirement cause remains matchable")
				require.ErrorIs(t, err, context.Canceled, "the original cancellation remains matchable")
				require.Equal(t, session.ErrChatHistoryUnavailable.Error(), err.Error(), "public error string exposes neither private cause")
			} else {
				readLogs := pagingLogCapture(t) // Includes a real bridge/file-sink positive control.
				before := len(readLogs())
				ctx := context.WithValue(cancelled, UserContextKey{}, &config.UserConfig{Username: "R1_USER_CANARY"})
				r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+f.sid+"/messages?"+r1CleanupQuery("cancellation"), nil).WithContext(ctx)
				w := httptest.NewRecorder()
				f.api.HandleSessions(w, r)
				logs := readLogs()[before:]
				position := pagingPositionOracle(f.sid, session.ArchiveAddress{PartitionKey: "2026-01-01", ByteOffset: 0, EntryID: "r1-old"})
				canaries := r1CleanupCanaries(f, position)
				t.Run("content_free_storage_summary", func(t *testing.T) {
					rows := pagingSummaryRows(logs)
					require.Len(t, rows, 1, "one actual page attempt emits exactly one content-free summary")
					pagingAssertSummary(t, rows[0], "storage", "latest", false, true, canaries)
					r1CleanupSummaryPrivacy(t, rows[0], canaries)
				})
				t.Run("normal_storage_log", func(t *testing.T) {
					var storage []map[string]any
					for _, row := range logs {
						if row["message"] == "rest: session storage failure" {
							storage = append(storage, row)
						}
					}
					require.Len(t, storage, 1, "B69: cancellation cannot suppress the real normal storage-failure log")
					require.Equal(t, "read", storage[0]["op"])
					require.Equal(t, f.sid, storage[0]["session_id"])
				})
				// The cancelled transport promises no HTTP status/body delivery.
				for _, canary := range []string{f.sid, f.retired, f.rolled, "R1_BODY_CANARY", "R1_RETAINED_BODY_CANARY", "R1_USER_CANARY", "R1_QUERY_CANARY"} {
					assert.NotContains(t, w.Body.String(), canary)
				}
			}
			require.FileExists(t, f.retired, "failed retry remains pending, not falsely retired")
			restore()
			r1CleanupHealthy(t, f, []string{"r1-current"})
			_, statErr := os.Stat(f.retired)
			require.ErrorIs(t, statErr, os.ErrNotExist, "a fresh ordinary read retries pending retirement after the actual fault clears")
		})
	}
}
