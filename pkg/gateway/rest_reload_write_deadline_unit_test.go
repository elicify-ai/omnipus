package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Observe the server/ResponseController boundary, not a mocked middleware.
// The follow-up requires an extension of at least reloadWaitTimeout.
type reloadDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
	setErr    error
}

func (w *reloadDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return w.setErr
}

type reloadDeadlineLogRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *reloadDeadlineLogRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *reloadDeadlineLogRecorder) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record.Clone())
	return nil
}
func (h *reloadDeadlineLogRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *reloadDeadlineLogRecorder) WithGroup(string) slog.Handler      { return h }
func (h *reloadDeadlineLogRecorder) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}

func TestReloadWaitWriteDeadlineMiddleware_ExtensionAndWarning(t *testing.T) {
	deadlineErr := errors.New("test connection rejected the write deadline")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"deadline_covers_entire_reload_wait", nil},
		{"deadline_error_warns_and_still_calls_handler", deadlineErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &reloadDeadlineLogRecorder{}
			previous := slog.Default()
			slog.SetDefault(slog.New(logs))
			t.Cleanup(func() { slog.SetDefault(previous) })
			writer := &reloadDeadlineRecorder{ResponseRecorder: httptest.NewRecorder(), setErr: tc.err}
			request := httptest.NewRequest(http.MethodPut, "/api/v1/agents", nil)
			calls := 0
			handler := reloadWaitWriteDeadlineMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				require.Len(t, writer.deadlines, 1, "set the deadline before dispatching the save")
				_, err := io.WriteString(w, "handler still answered\n")
				require.NoError(t, err)
			}))
			started := time.Now()
			handler.ServeHTTP(writer, request)

			require.Equal(t, 1, calls, "a failed deadline update must not swallow the save handler")
			require.Len(t, writer.deadlines, 1, "attempt exactly one deadline extension")
			require.GreaterOrEqual(t, writer.deadlines[0].Sub(started), reloadWaitTimeout,
				"the actual deadline must cover at least the whole reload wait")
			require.Equal(t, http.StatusOK, writer.Code)
			require.Equal(t, "handler still answered\n", writer.Body.String())
			records := logs.snapshot()
			if tc.err == nil {
				require.Empty(t, records, "a successful deadline change must not warn")
				return
			}
			require.Len(t, records, 1, "a failed deadline change must emit one diagnostic")
			require.Equal(t, slog.LevelWarn, records[0].Level)
			require.Equal(t, "rest: could not extend write deadline for reload wait", records[0].Message)
			attributes := make(map[string]string)
			records[0].Attrs(func(attribute slog.Attr) bool {
				attributes[attribute.Key] = attribute.Value.String()
				return true
			})
			require.Equal(t, map[string]string{"path": request.URL.Path, "error": deadlineErr.Error()}, attributes)
		})
	}
}

func TestWaitsForReload_MethodAndPathBoundaries(t *testing.T) {
	// Expected classes come from follow-up item 3 and the original save brief.
	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"PUT_API", http.MethodPut, "/api/v1/agents", true},
		{"POST_API", http.MethodPost, "/api/v1/agents", true},
		{"DELETE_API", http.MethodDelete, "/api/v1/agents/test-agent", true},
		{"PATCH_API", http.MethodPatch, "/api/v1/agents/test-agent", true},
		{"GET_API", http.MethodGet, "/api/v1/agents", false},
		{"HEAD_API", http.MethodHead, "/api/v1/agents", false},
		{"OPTIONS_API", http.MethodOptions, "/api/v1/agents", false},
		{"API_root_with_slash", http.MethodPut, "/api/v1/", true},
		{"API_root_without_slash", http.MethodPut, "/api/v1", false},
		{"different_API_version", http.MethodPut, "/api/v10/x", false},
		{"API_substring_is_not_prefix", http.MethodPut, "/outside/api/v1/agents", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			require.Equal(t, tc.want, waitsForReload(request), "%s %s reload-wait classification", tc.method, tc.path)
		})
	}
}

func TestReloadWaitConnectionDrop_OnlyEOFOrReset(t *testing.T) {
	// Follow-up item 4 permits exactly these two network-close results, not
	// timeouts, incomplete HTTP bodies or unrelated transport failures.
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"EOF", io.EOF, true},
		{"wrapped_EOF", fmt.Errorf("HTTP client: %w", io.EOF), true},
		{"reset", syscall.ECONNRESET, true},
		{"wrapped_reset", fmt.Errorf("read TCP: %w", syscall.ECONNRESET), true},
		{"nil_is_not_drop", nil, false},
		{"unexpected_EOF_is_not_drop", io.ErrUnexpectedEOF, false},
		{"deadline_is_not_drop", context.DeadlineExceeded, false},
		{"wrapped_deadline_is_not_drop", fmt.Errorf("HTTP client: %w", context.DeadlineExceeded), false},
		{"refused_connection_is_not_drop", syscall.ECONNREFUSED, false},
		{"arbitrary_error_is_not_drop", errors.New("connection dropped"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isReloadWaitConnectionDrop(tc.err))
		})
	}
}
