//go:build !cgo

// rest_executor_smoketest_rawlog_test.go — MEDIUM regression test.
//
// sanitizeSmokeTestErrorMessage strips raw provider/CLI text from the
// operator-visible smoke-test response body (see
// TestPostAgentsExecutorSmokeTest_FatalErrorEvent in
// rest_executor_smoketest_test.go), and the sanitized copy tells the
// operator to "see gateway.log for details". Before this fix,
// drainSmokeTestRun discarded the raw message the instant it sanitized it,
// and the "run finished" completion log omitted the error entirely — so an
// operator following that instruction found nothing in gateway.log. This
// test captures the real slog output around a failing run and asserts the
// raw CLI text IS present server-side, even though it never appears in the
// HTTP response.
//
// Re-review FIX 1: the raw-error log line (rest_executor_smoketest.go's
// drainSmokeTestRun) was itself a bare slog.Warn — which this test's
// original slog.SetDefault capture made LOOK covered, but slog.SetDefault
// is never called anywhere outside test code in this repo, so on a real,
// backgrounded gateway (`./omnipus gateway --allow-empty &`) that line went
// to log/slog.Default()'s zero-value stderr-only handler, never reaching
// $OMNIPUS_HOME/logs/gateway.log — making the response's own "See
// gateway.log for details" a dead end. The fix routes that specific call
// through pkg/logger (the sink logger.EnableFileLogging actually wires to
// gateway.log), so this test now captures it the same way — see
// pkg/workspace/find_for_agent_test.go for the established
// EnableFileLogging test-capture pattern this mirrors. The OTHER assertion
// in this test (the "run finished" completion log line) is untouched by
// FIX 1 and still goes through slog.Default(), so it keeps using the
// original slog.SetDefault capture.

package gateway

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

func TestPostAgentsExecutorSmokeTest_RawErrorLoggedServerSide(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	var logBuf bytes.Buffer
	oldHandler := slog.Default().Handler()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(slog.New(oldHandler))

	// FIX 1: the raw error text no longer goes through slog at all — it
	// goes through pkg/logger, which has no exported io.Writer sink for its
	// console logger, so (matching the established pattern in e.g.
	// pkg/workspace/find_for_agent_test.go) redirect through
	// EnableFileLogging and read the file back afterward.
	logFile := filepath.Join(t.TempDir(), "executor-smoketest-rawlog.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	const rawErr = "boom happened raw provider text 4f8c9"
	stub := writeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"result","subtype":"error","error":"boom happened raw provider text 4f8c9"}'
exit 0
`)
	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, stub)

	code, resp := postExecutorSmokeTest(t, api, body)
	require.Equal(t, http.StatusOK, code)
	require.False(t, resp.Ok)
	require.NotNil(t, resp.Error)

	logged := logBuf.String()

	// The raw text must NOT be in the HTTP response (already covered by
	// TestPostAgentsExecutorSmokeTest_FatalErrorEvent — reasserted here for
	// self-containment / to document WHY the raw text must instead be
	// findable in the log).
	assert.NotEqual(t, rawErr, *resp.Error)

	// It MUST be discoverable server-side, or "See gateway.log for
	// details" is a dead end for the operator following that instruction.
	// This is the actual gateway.log-equivalent sink (pkg/logger's file
	// logger), not the slog buffer above — see the FIX 1 note in this
	// file's header comment for why those are no longer the same thing for
	// this specific line.
	fileLogged, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.Contains(t, string(fileLogged), rawErr,
		"the raw CLI error text must be logged server-side (gateway.log) even though it is stripped from the response")

	// The completion log line must also carry at least the sanitized error
	// text, so a run_id grep finds a "why" without needing to also find the
	// separate raw-error log line. This line is untouched by FIX 1 (still a
	// bare slog.Info at rest_executor_smoketest.go's runExecutorSmokeTest),
	// so it is still captured via the slog buffer.
	assert.Contains(t, logged, "run finished")
	assert.Contains(t, logged, *resp.Error)
}
