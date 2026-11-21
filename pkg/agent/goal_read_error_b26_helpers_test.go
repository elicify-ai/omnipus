package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// #1857 B26 (founder sec2 A; spec §13.8.2 B26 and §13.8.5 B26, oracle T-55):
// a goal-state read failure shows the user only the fixed
// goal_state_unavailable catalogue sentence, in the live frame and in the saved
// transcript line. The operation and the raw error go to the server log line
// "goal_read_error: ..." only. The helpers below are the shared observation
// points of the goal-reminder tests that used to read the raw text out of the
// frame or the saved line.

// b26Sentence is the catalogue sentence, taken from the generated catalogue.
func b26Sentence(t *testing.T) string {
	t.Helper()
	sentence := generated.LLMErrorUserMessages["goal_state_unavailable"]
	require.NotEmpty(t, sentence, "SETUP: the generated catalogue must carry goal_state_unavailable")
	return sentence
}

// b26EnableServerLog captures the server log through the real file sink (the
// gateway.log writer) and returns its path.
func b26EnableServerLog(t *testing.T) string {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "b26-serverlog.log")
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() { logger.DisableFileLogging() })
	return logFile
}

// b26RequireNoRaw fails when text carries any raw fragment.
func b26RequireNoRaw(t *testing.T, where, text string, raws ...string) {
	t.Helper()
	for _, raw := range raws {
		assert.NotContains(t, text, raw,
			"#1857 B26: raw error text %q must not reach %s (the user sees only the goal_state_unavailable sentence)", raw, where)
	}
}

// b26RequireLogLine requires the dedicated goal_read_error server log line to
// carry both the operation and the raw error fragment on the same line.
func b26RequireLogLine(t *testing.T, logFile, operation, raw string) {
	t.Helper()
	logBytes, err := os.ReadFile(logFile)
	require.NoError(t, err, "SETUP: reading the captured server log")
	for _, line := range strings.Split(string(logBytes), "\n") {
		if strings.Contains(line, "goal_read_error: ") && strings.Contains(line, operation) && strings.Contains(line, raw) {
			return
		}
	}
	t.Fatalf("#1857 B26: the server log must carry a goal_read_error line with the operation %q and the error %q", operation, raw)
}

// b26RequireSavedLines requires that no saved row carries a raw fragment, and
// returns how many saved rows hold exactly the catalogue sentence.
func b26RequireSavedLines(t *testing.T, rows []session.TranscriptEntry, raws ...string) int {
	t.Helper()
	sentence := b26Sentence(t)
	saved := 0
	for _, row := range rows {
		b26RequireNoRaw(t, "a saved transcript line", row.Content, raws...)
		if row.Role == "system" && row.Content == sentence {
			saved++
		}
	}
	return saved
}
