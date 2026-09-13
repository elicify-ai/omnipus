package gateway

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Keep the real ingest reader and heartbeat admission. Capture only the logging
// boundary: the console formatter renders newline-bearing attributes literally.
type browserReasonLogCapture struct {
	reasons chan string
}

func (h *browserReasonLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *browserReasonLogCapture) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "capture-ingest: encoder reported a stream-quality failure" {
		record.Attrs(func(attribute slog.Attr) bool {
			if attribute.Key == "reason" {
				h.reasons <- attribute.Value.String()
			}
			return true
		})
	}
	return nil
}
func (h *browserReasonLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *browserReasonLogCapture) WithGroup(string) slog.Handler      { return h }

func TestBrowserCaptureReasonLogValueCannotInjectLines(t *testing.T) {
	capture := &browserReasonLogCapture{reasons: make(chan string, 2)}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	captureSession, client, _, _ := captureTransportFixture(t)
	require.NotNil(t, captureSession)
	for _, sample := range []struct{ reason, expected string }{
		{"encoder failed\nFORGED level=info\r\t\x1b[2J", `encoder failed\nFORGED level=info\r\t\x1b[2J`},
		{"encoder failed: café — retry", "encoder failed: café — retry"},
	} {
		require.NoError(t, client.WriteJSON(map[string]any{
			"type": "browser_capture_control", "action": "ping", "reason": sample.reason,
		}))
		select {
		case actual := <-capture.reasons:
			require.Equal(t, sample.expected, actual, "browser-origin diagnostic must be printable on one console line")
		case <-time.After(time.Second):
			t.Fatal("real ingest did not log accepted heartbeat reason")
		}
	}
}

func TestBrowserLogValueEscapesConsoleControls(t *testing.T) {
	for _, sample := range []struct{ name, value, expected string }{
		{"ordinary", "encoder failed: café — retry", "encoder failed: café — retry"},
		{"empty", "", ""},
		{"controls", "a\n\r\t\x1bb", `a\n\r\t\x1bb`},
		{"separators", "a\u2028b\u2029c", `a\u2028b\u2029c`},
		{"quoted escape", `"a\nFORGED"`, `\"a\\nFORGED\"`},
		{"literal backslash", `a\n`, `a\\n`},
		{"quoted newline", "\"a\nFORGED\"", `\"a\nFORGED\"`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			actual := browserLogValue(sample.value)
			require.Equal(t, sample.expected, actual)
			// Match the console formatter's attempted unquote: normalization must
			// not leave a quoted string that can reactivate control escapes.
			if unquoted, err := strconv.Unquote(actual); err == nil {
				actual = unquoted
			}
			require.False(t, strings.ContainsAny(actual, "\n\r\t\x1b\u2028\u2029"))
		})
	}
}
