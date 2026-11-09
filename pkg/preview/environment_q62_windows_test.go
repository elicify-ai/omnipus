//go:build windows

package preview

import (
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/stretchr/testify/require"
)

// Windows has Job Objects, not a kernel filesystem backend. This is not an
// unconfined-platform skip; the same real launcher/converter bodies still run.
func q62Prime(t *testing.T) bool {
	t.Helper()
	return true
}

// Q62 / F1 / MV-2 and Go os/exec's documented addCriticalEnv semantics.
// This MUST run natively on Windows to prove CreateProcess behaviour; vet on
// a Mac is only static evidence and is never reported as Windows execution.
func TestPreviewQ62_WindowsFinalSystemRootSemantics(t *testing.T) {
	q62PlantParentSentinels(t)
	self, err := os.Executable()
	require.NoError(t, err)
	root := os.Getenv("SYSTEMROOT")
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"implicit_from_nil", nil}, {"implicit_from_empty", []string{}},
		{"explicit_SystemRoot", []string{"SystemRoot=" + root}},
		{"explicit_SYSTEMROOT", []string{"SYSTEMROOT=" + root}},
		{"explicit_mixed_case", []string{"sYsTeMrOoT=" + root}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := launcher.Command(launcher.Spec{Self: self, Target: self, Env: tc.env})
			require.NoError(t, err)
			require.NotNil(t, before.Env)
			q62AssertEnvironment(t, before.Env, tc.env)
			proc, err := launcher.Start(launcher.Spec{Self: self, Target: self, Args: []string{"__q62-env-dump"}, Env: tc.env})
			require.NoError(t, err)
			guard := time.AfterFunc(20*time.Second, func() { proc.Kill("test-deadline") })
			defer guard.Stop()
			defer proc.Kill("test-cleanup")
			require.NoError(t, proc.Go())
			require.NoError(t, proc.Stdin.Close())
			raw, readErr := io.ReadAll(proc.Stdout)
			require.NoError(t, readErr)
			require.NoError(t, proc.Wait())
			var receipt q62TargetReceipt
			require.NoError(t, json.Unmarshal(raw, &receipt))
			require.Equal(t, "final-target", receipt.Stage)
			require.Equal(t, "dump", receipt.Program)
			require.NotEqual(t, os.Getpid(), receipt.PID)
			q62AssertEnvironment(t, receipt.Env, []string{"SYSTEMROOT=" + root})
		})
	}
}
