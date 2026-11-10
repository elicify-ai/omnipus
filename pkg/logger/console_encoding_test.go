package logger

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// Regression contract from the console log output encoding brief, job item 4:
// ordinary username text must remain intact, with embedded newlines encoded
// rather than rendered as additional console log lines.
func TestConsoleWriter_EncodesNewlinesInOrdinaryStringFields(t *testing.T) {
	initialGlobalLevel := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	t.Cleanup(func() { zerolog.SetGlobalLevel(initialGlobalLevel) })

	tests := []struct {
		name          string
		username      string
		expectedField string
	}{
		{
			name:          "plain username control",
			username:      "alice",
			expectedField: "username=alice",
		},
		{
			name:     "embedded newline stays encoded",
			username: "alice\n09:31:00 INF session started",
			// Safe quoted text required by the brief, not the current formatter's output.
			expectedField: `username="alice\n09:31:00 INF session started"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			// Exercise the same construction that production init uses. Only the
			// output sink, color and time zone change; the real field hook stays wired.
			consoleWriter := newConsoleWriter(&output)
			consoleWriter.NoColor = true
			consoleWriter.TimeLocation = time.UTC
			consoleLogger := zerolog.New(consoleWriter)

			// Fixed fixture time makes the complete event deterministic without
			// replacing the clock or adapting the expected text to observed output.
			eventTime := time.Date(2026, time.October, 1, 9, 30, 0, 0, time.UTC)
			consoleLogger.Info().
				Time(zerolog.TimestampFieldName, eventTime).
				Str("username", tt.username).
				Msg("authentication attempt")

			got := output.String()
			want := "09:30:00 INF authentication attempt " + tt.expectedField + "\n"
			if got != want {
				t.Errorf("console event must preserve and safely encode the username:\n- want: %q\n+ got:  %q\nraw output:\n%s", want, got, got)
			}
			// One log call permits one raw newline, its final event terminator.
			if rawNewlines := strings.Count(got, "\n"); rawNewlines != 1 {
				t.Errorf("one console event has %d raw newlines, want 1 (only the event terminator); output=%q", rawNewlines, got)
			}
		})
	}
}
