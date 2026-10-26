//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const r2ExternalProcessEnv = "ELICIFY_CRASH_R2_EXTERNAL_PROCESS"

// Only the external-process edge is scripted. The test binary serves as a
// portable vendor CLI fixture before testing.Main runs; it does not start a
// second test suite. The actual CodexDriver counts the emitted completed turns,
// detects its own configured ceiling and supplies the real drain/completion path.
func init() {
	mode := os.Getenv(r2ExternalProcessEnv)
	if mode == "" {
		return
	}
	for _, arg := range os.Args[1:] {
		if arg == "--version" {
			if _, err := fmt.Fprintln(os.Stdout, "codex-cli 1.0.0-r2-fixture"); err != nil {
				os.Exit(3)
			}
			os.Exit(0)
		}
	}
	if err := os.WriteFile(os.Getenv("ELICIFY_CRASH_R2_RECEIPT"), []byte("real external process: "+mode), 0600); err != nil {
		os.Exit(4)
	}
	lines := []string{
		`{"type":"thread.started","thread_id":"r2-real-driver-thread"}`,
		`{"type":"item.completed","item":{"id":"partial","type":"agent_message","text":"external partial output"}}`,
	}
	if mode == "cap" {
		lines = append(lines, `{"type":"turn.completed"}`, `{"type":"turn.completed"}`)
	} else {
		// Deliberately limit-looking prose is NOT a typed, code-owned limit.
		lines = append(lines, `{"type":"error","message":"ordinary external failure: turn cap exceeded is only vendor text"}`)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(os.Stdout, line); err != nil {
			os.Exit(5)
		}
	}
	os.Exit(0)
}

// A4 / DESIGN 1: every positively code-identified ceiling freezes Interrupted /
// tool_limit with a nonfatal outcome. Arbitrary vendor error prose stays Failed.
func TestCrashRecovery_R2ExternalDriverCeilingIsInterrupted(t *testing.T) {
	for _, mode := range []string{"cap", "ordinary_error_control"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(r2ExternalProcessEnv, mode)
			provider := &delegateDispatchProvider{}
			al := newDelegateDispatchLoop(t, provider)
			pwWireRuntime(t, al) // Real audience resolver/outbox deliverer, not the older fixture's unwired tail.
			binary, err := os.Executable()
			require.NoError(t, err)
			receipt := filepath.Join(t.TempDir(), "external-process.receipt")
			t.Setenv("ELICIFY_CRASH_R2_RECEIPT", receipt)
			inst, found := al.GetRegistry().GetAgent(delegateExtCLIAgentID)
			require.True(t, found)
			inst.MaxIterations = 1 // Two emitted turns cross the actual driver cap.
			inst.Subagents = &config.SubagentsConfig{Executor: &config.ExecutorConfig{
				Kind: config.ExecutorKindExternalCLI, CLI: "codex", CLIPath: binary,
				EnvOverrides: map[string]string{r2ExternalProcessEnv: mode, "ELICIFY_CRASH_R2_RECEIPT": receipt},
			}}
			launched := launchDelegateTo(t, al, delegateExtCLIAgentID, "perform external work with a one-turn ceiling")
			before, err := al.GetSessionLifecycleStore().Load(launched.SessionID)
			require.NoError(t, err)
			dispatch, err := NewSteerLauncher(al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
			require.NoError(t, err)
			require.Equal(t, steer.DispatchRunning, dispatch.State)
			record, err := al.GetSessionLifecycleStore().Load(launched.SessionID)
			require.NoError(t, err)
			if owner := al.executionDispositionFor(al.executionClaimFor(record)); owner != nil {
				crashAwaitDone(t, owner.done)
			}
			record, err = al.GetSessionLifecycleStore().Load(launched.SessionID)
			require.NoError(t, err)
			proof, err := os.ReadFile(receipt)
			require.NoError(t, err, "instrument: actual driver started the controlled external process")
			require.Equal(t, "real external process: "+mode, string(proof))
			assert.Zero(t, provider.calls.Load(), "native loop is not substituted for the real external runner")
			assert.Equal(t, before.Generation, record.Generation)
			assert.NotNil(t, record.FinalDelivery, "real completion/outbox saved the external outcome")
			assert.Nil(t, al.getActiveTurnState(launched.SessionID), "ended external execution releases its active turn")
			if mode == "cap" {
				assert.Equal(t, session.LifecycleInterrupted, record.State, "code-detected external cap is not generic Failed")
				assert.Equal(t, session.InterruptionToolLimit, record.InterruptionReason)
				assert.Empty(t, record.FailedReason)
			} else {
				assert.Equal(t, session.LifecycleFailed, record.State, "arbitrary vendor prose is never guessed into a ceiling")
				assert.Empty(t, record.InterruptionReason)
			}
			messages, _, more, err := al.GetMessageInboxStore().Drain(before.SteeringSessionID(), record.SessionID, "", 10)
			require.NoError(t, err)
			require.False(t, more)
			require.Len(t, messages, 1, "one real upward result")
			outcome, err := messages[0].AsSessionMessageError()
			require.NoError(t, err)
			assert.Equal(t, mode != "cap", outcome.Fatal, "only a genuine external failure is fatal")
		})
	}
}
