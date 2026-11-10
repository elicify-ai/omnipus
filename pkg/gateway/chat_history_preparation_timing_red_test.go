package gateway

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Only outward HTTP delivery is blocked. The handler, reader, projection,
// generated carriers, attempt accounting and production log sink stay real.
type historyPhaseBlockedWriter struct {
	recorder               *httptest.ResponseRecorder
	entered                chan time.Time
	release                chan struct{}
	enterOnce, releaseOnce sync.Once
}

func (w *historyPhaseBlockedWriter) Header() http.Header  { return w.recorder.Header() }
func (w *historyPhaseBlockedWriter) WriteHeader(code int) { w.recorder.WriteHeader(code) }
func (w *historyPhaseBlockedWriter) Write(body []byte) (int, error) {
	w.enterOnce.Do(func() {
		w.entered <- time.Now() // Actual first body delivery begins after preparation.
		<-w.release
	})
	return w.recorder.Write(body)
}
func (w *historyPhaseBlockedWriter) unblock() { w.releaseOnce.Do(func() { close(w.release) }) }

func historyPhaseResponse(t *testing.T, sid, carrier string, response *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, "%s", response.Body.Bytes())
	if carrier == "rest_messages" {
		page := historyMigrationPage(t, sid, response.Body.Bytes(), "latest", false, false)
		require.Equal(t, []string{"timing-001", "timing-002"}, pagingIDs(page))
	} else {
		detail := historyMigrationDetail(t, sid, response.Body.Bytes(), false)
		require.Len(t, *detail.Messages, 2)
		require.Equal(t, "timing-001", (*detail.Messages)[0].Id)
		require.Equal(t, "timing-002", (*detail.Messages)[1].Id)
	}
}

func historyPhaseDuration(t *testing.T, row map[string]any, carrier string) float64 {
	t.Helper()
	require.Equal(t, "history_page", row["message"])
	require.Equal(t, "history_page", row["event"])
	require.Equal(t, carrier, row["carrier"])
	require.Equal(t, "latest", row["mode"])
	require.Equal(t, "success", row["outcome"])
	duration, durationOK := row["duration_ms"].(float64)
	require.True(t, durationOK, "a real measured duration, never missing-as-zero")
	require.False(t, math.IsNaN(duration) || math.IsInf(duration, 0))
	require.GreaterOrEqual(t, duration, float64(0))
	return duration
}

// Spec Content-free observability: duration_ms is preparation/validation ONLY.
// This compares actual phase timestamps, not a performance threshold: a timer
// plants delivery latency longer than the observed preparation upper bound.
func TestHistoryPageDiagnostics_PreparationDurationExcludesBlockedDelivery(t *testing.T) {
	for _, scenario := range []struct{ carrier, suffix string }{{"rest_messages", "/messages"}, {"rest_detail", ""}} {
		t.Run(scenario.carrier, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			sid := createTestSession(t, api)
			pagingSeed(t, api.agentLoop.GetSessionStore(), sid, "timing", 2)
			readLogs := pagingLogCapture(t) // Includes its real bridge/file-sink control.
			before := len(pagingSummaryRows(readLogs()))
			path := "/api/v1/sessions/" + sid + scenario.suffix
			control := httptest.NewRecorder()
			controlStart := time.Now()
			api.HandleSessions(control, httptest.NewRequest(http.MethodGet, path, nil))
			controlElapsed := time.Since(controlStart)
			historyPhaseResponse(t, sid, scenario.carrier, control)
			controlRows := pagingSummaryRows(readLogs())
			require.Len(t, controlRows, before+1, "one real successful summary in the unblocked instrument control")
			require.LessOrEqual(t, historyPhaseDuration(t, controlRows[before], scenario.carrier), float64(controlElapsed.Nanoseconds())/1e6)

			writer := &historyPhaseBlockedWriter{recorder: httptest.NewRecorder(), entered: make(chan time.Time, 1), release: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			t.Cleanup(func() {
				writer.unblock()
				cancel()
				select {
				case <-done:
				case <-time.After(30 * time.Second):
					t.Error("owned blocked-delivery handler did not actually join")
				}
			})
			request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
			callStarted := time.Now()
			go func() { defer close(done); api.HandleSessions(writer, request) }()
			var deliveryStarted time.Time
			select {
			case deliveryStarted = <-writer.entered:
			case <-done:
				t.Fatal("real handler never entered body delivery; not a timing RED")
			case <-time.After(30 * time.Second):
				t.Fatal("real handler did not reach the delivery barrier; not a timing RED")
			}
			preDeliveryUpper := deliveryStarted.Sub(callStarted)
			require.Positive(t, preDeliveryUpper)
			// Deliberately injected transport latency, NOT an elapsed-time oracle
			// for ordering/dispatch. Any scheduler delay only strengthens the input.
			transportDelay := time.NewTimer(4 * preDeliveryUpper)
			defer transportDelay.Stop()
			<-transportDelay.C
			deliveryEnded := time.Now()
			writer.unblock()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("released real handler did not join; not a duration assertion")
			}
			cancel()
			require.GreaterOrEqual(t, deliveryEnded.Sub(deliveryStarted), 4*preDeliveryUpper, "instrument actually held outward delivery")
			historyPhaseResponse(t, sid, scenario.carrier, writer.recorder)
			rows := pagingSummaryRows(readLogs())
			require.Len(t, rows, before+2, "EXACTLY one summary for the blocked request; never per write/candidate/carrier layer")
			duration := historyPhaseDuration(t, rows[before+1], scenario.carrier)
			upperMS := float64(preDeliveryUpper.Nanoseconds()) / 1e6
			t.Logf("PREPARATION_TIMING_CONTROLS=PASS carrier=%s pre_delivery_upper_ms=%g held_delivery_ms=%g reported_duration_ms=%g summaries=1", scenario.carrier, upperMS, float64(deliveryEnded.Sub(deliveryStarted).Nanoseconds())/1e6, duration)
			require.LessOrEqual(t, duration, upperMS, "preparation timing RED: preparation/validation ends before the first body write; blocked delivery must never inflate duration_ms")
		})
	}
}
