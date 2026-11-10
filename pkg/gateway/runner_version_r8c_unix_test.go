//go:build darwin || linux

package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Q59: a successful process exit is not a version. The actual first output
// line must never become cli_version or any other part of the HTTP response.
func TestR8c_RunnerNoVersionFirstLineNeverReachesResponse_Q59(t *testing.T) {
	f, program, marker := r8PersonProgram(t)
	_, control := r8RunnerResponse(t, f)
	require.True(t, control.Ok, "instrument: the same checked/saved program and route first report a real version")
	require.Equal(t, "8.1.0", deref(control.CliVersion))
	require.NoError(t, os.Remove(marker))
	const firstLine = "unversioned-output-private-marker"
	quotedMarker := "'" + strings.ReplaceAll(marker, "'", "'\"'\"'") + "'"
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + quotedMarker + "\nprintf '%s\\n' '" + firstLine + "'\nexit 0\n"
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	r8RequireCheckedProgram(t, f, program)

	w, result := r8RunnerResponse(t, f)
	invocations, err := os.ReadFile(marker)
	require.NoError(t, err, "instrument: the unversioned program actually ran")
	assert.Equal(t, "--version\n", string(invocations), "exactly the checked version handshake ran, not real agent work")
	assert.False(t, result.Ok, "Q59: an exit-zero process without a version does not complete the version handshake")
	assert.Equal(t, gen.RunnerTestResponseReasonHandshakeFailed, result.Reason)
	assert.Nil(t, result.CliVersion, "Q59: a first line without a version must not populate cli_version")
	assert.NotEmpty(t, result.Message, "the failed connection still has fixed client text")
	r8AssertCleanDiagnosticBody(t, w.Body.Bytes(), firstLine, program, filepath.Dir(program))
}
