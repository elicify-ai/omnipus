//go:build darwin || linux

package gateway

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder Q50 A / security F1: same failure class yields fixed client text,
// independent of the program's output. Diagnostics are synthetic local text.
func TestR8_RunnerHandshakeFailureUsesFixedClientText_Q50(t *testing.T) {
	var messages []string
	for _, label := range []string{"first", "second"} {
		t.Run(label, func(t *testing.T) {
			f, program, _ := r8PersonProgram(t)
			_, control := r8RunnerResponse(t, f)
			require.True(t, control.Ok, "instrument: the same saved program first completes its real handshake")
			diagnostic := "r8-" + label + "-private-file.txt: permission denied"
			script := "#!/bin/sh\nprintf '%s\\n' '" + diagnostic + "' >&2\nexit 23\n"
			require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
			r8RequireCheckedProgram(t, f, program)
			out, err := exec.CommandContext(t.Context(), program, "--version").CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, "instrument: the local diagnostic is an exit failure, not a start failure")
			require.Equal(t, 23, exit.ExitCode())
			require.Equal(t, diagnostic+"\n", string(out), "instrument: the program really emitted this private diagnostic")
			w, result := r8RunnerResponse(t, f)
			assert.False(t, result.Ok)
			assert.Equal(t, gen.RunnerTestResponseReasonHandshakeFailed, result.Reason)
			assert.Equal(t, "claude-code", deref(result.Cli))
			assert.Nil(t, result.CliVersion)
			assert.NotEmpty(t, result.Message, "Q50: failure still has a client message; its class is pinned by reason")
			r8AssertCleanDiagnosticBody(t, w.Body.Bytes(), diagnostic, "r8-"+label+"-private-file.txt", "exit status",
				program, filepath.Dir(program))
			messages = append(messages, result.Message)
		})
	}
	require.Len(t, messages, 2)
	assert.Equal(t, messages[0], messages[1], "Q50: changing raw output must not change the fixed classified client message")
}

// Q50 separately covers a regular executable that passes location validation
// but cannot start. The missing interpreter is private to this test sandbox.
func TestR8_RunnerStartFailureUsesFixedClientText_Q50(t *testing.T) {
	var messages []string
	for _, label := range []string{"first", "second"} {
		t.Run(label, func(t *testing.T) {
			f, program, _ := r8PersonProgram(t)
			_, control := r8RunnerResponse(t, f)
			require.True(t, control.Ok)
			interpreter := filepath.Join(filepath.Dir(program), "r8-"+label+"-missing-interpreter")
			require.NoFileExists(t, interpreter)
			require.NoError(t, os.WriteFile(program, []byte("#!"+interpreter+"\n"), 0o755))
			r8RequireCheckedProgram(t, f, program)
			command := exec.CommandContext(t.Context(), program, "--version")
			startErr := command.Start()
			if startErr == nil {
				waitErr := command.Wait()
				t.Fatalf("SETUP: fixture must fail to start, instead it started and Wait returned %v", waitErr)
			}
			var pathErr *os.PathError
			require.ErrorAs(t, startErr, &pathErr, "instrument: this is a start error after successful file checks")
			require.True(t, errors.Is(startErr, os.ErrNotExist), "instrument: synthetic interpreter is absent: %v", startErr)
			require.True(t, strings.Contains(startErr.Error(), program), "instrument: the raw start error contains the checked path")
			w, result := r8RunnerResponse(t, f)
			assert.False(t, result.Ok)
			assert.Equal(t, gen.RunnerTestResponseReasonHandshakeFailed, result.Reason,
				"contract: an existing checked file that cannot start is a handshake failure")
			assert.Equal(t, "claude-code", deref(result.Cli))
			assert.Nil(t, result.CliVersion)
			assert.NotEmpty(t, result.Message, "Q50: failure still has a client message; its class is pinned by reason")
			r8AssertCleanDiagnosticBody(t, w.Body.Bytes(), startErr.Error(), program, filepath.Dir(program), interpreter, "fork/exec")
			messages = append(messages, result.Message)
		})
	}
	require.Len(t, messages, 2)
	assert.Equal(t, messages[0], messages[1], "Q50: different OS start diagnostics produce the same fixed classified message")
}
