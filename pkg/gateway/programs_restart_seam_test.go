package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// R10: real registered restart handler, process-edge restarter only replaced.
// Waits for actual asynchronous completion; never re-execs the test process.
func TestProgramCredential_RestartSeamJoined(t *testing.T) {
	f := pgPersonAPI(t)
	w := pgSweepServe(t, f, http.MethodPost, "/api/v1/gateway/restart", `{}`, f.token, "")
	require.Equal(t, http.StatusAccepted, w.Code, "%s", w.Body.String())
	pgAwaitRestart(t, f)
	require.Equal(t, int32(1), f.restartRequests())
}
