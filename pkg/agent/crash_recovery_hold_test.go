//go:build goolm && stdjson

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// DESIGN Decision 2 / Q15: Unknown is sticky across reboot; only a NEW
// explicitly authorized continuation after its report accepts that uncertainty.
func TestCrashRecovery_UnresolvedHoldSurvivesBootAndAllowsExplicitContinuation(t *testing.T) {
	for _, name := range []string{"append_file", "write_file", "edit_file"} {
		t.Run(name, func(t *testing.T) {
			f := crashFileCut(t, name)
			require.NoError(t, os.RemoveAll(filepath.Join(f.snapshot, "sessions", f.record.SessionID, "file_effects")))
			p := &crashScript{entered: make(chan int, 8)}
			f.h.restore(t, f.snapshot, p)
			f.h.recover(t)
			held := f.h.record(t, f.record.SessionID)
			require.Equal(t, session.LifecycleState("interrupted"), held.State, "Q15: first recovery must report and freeze the unresolved occurrence before continuing")
			require.Equal(t, "reconciliation_required", crashFields(t, held)["interruption_reason"])
			for range 2 {
				quiet := &crashScript{entered: make(chan int, 8)}
				f.h.reboot(t, quiet)
				after := f.h.record(t, f.record.SessionID)
				assert.Equal(t, held.Generation, after.Generation, "boot is not acceptance of uncertainty")
				assert.Equal(t, held.ExecutionID, after.ExecutionID, "boot cannot start a replacement execution for a held occurrence")
				assert.Equal(t, session.LifecycleState("interrupted"), after.State)
				assert.Equal(t, "reconciliation_required", crashFields(t, after)["interruption_reason"])
				assert.Equal(t, 0, len(quiet.calls()), "repeated boot cannot auto-release a recorded Unknown hold")
			}
			bytes, err := os.ReadFile(f.path)
			require.NoError(t, err)
			assert.Equal(t, f.after, string(bytes), "reboots perform no target writes")
			gate := make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-gate:
				default:
					close(gate)
				}
			})
			explicit := &crashScript{entered: make(chan int, 8), gate: gate}
			explicit.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
				if i == 0 {
					return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("accepted-new-write", "append_file", map[string]any{"path": f.path, "content": "EXPLICIT\n"})}, FinishReason: "tool_calls"}, nil
				}
				return &providers.LLMResponse{Content: "explicit new work completed", FinishReason: "stop"}, nil
			}
			f.h.al.GetRegistry().primaryChatAgentForTest().Provider = explicit
			gen, err := NewSteerCanceller(f.h.al.GetSessionLifecycleStore()).Revive(context.Background(), held.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "owner"})
			require.NoError(t, err, "new direct authorized continuation remains reachable after the report")
			require.Equal(t, held.Generation+1, gen)
			dispatch, err := NewSteerLauncher(f.h.al).Dispatch(context.Background(), held.SessionID, gen)
			require.NoError(t, err)
			require.Equal(t, steer.DispatchRunning, dispatch.State)
			crashAwait(t, explicit.entered)
			fresh := f.h.record(t, held.SessionID)
			assert.NotEqual(t, held.ExecutionID, fresh.ExecutionID)
			owner := f.h.al.executionDispositionFor(f.h.al.executionClaimFor(fresh))
			require.NotNil(t, owner)
			close(gate)
			crashAwaitDone(t, owner.done)
			bytes, err = os.ReadFile(f.path)
			require.NoError(t, err)
			assert.Equal(t, f.after+"EXPLICIT\n", string(bytes), "only the explicitly accepted new mutation executes; old effect is never replayed")
		})
	}
}
