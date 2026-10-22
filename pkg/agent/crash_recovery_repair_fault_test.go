//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/testhooks"
)

// Fault only the durable filesystem append boundary; retain the real store's
// archive reads, snapshot, address mapping and projection. Recovery, assembly,
// validation and the HTTP provider adapter are never replaced.
type crashRecoveryWriteFault struct {
	*session.UnifiedStore
	attempts int
	failure  error
}

func (s *crashRecoveryWriteFault) AddMessage(string, string, string) error {
	s.attempts++
	return s.failure
}
func (s *crashRecoveryWriteFault) AppendModelMessage(context.Context, string, session.ModelAppend) (session.ModelSlot, session.WindowView, error) {
	s.attempts++
	return session.ModelSlot{}, session.WindowView{}, s.failure
}

func TestCrashRecovery_RepairWriteFailureBlocksProviderInsteadOfHidingBatch(t *testing.T) {
	h := cwR1New(t, 100000)
	h.append(t, providers.Message{Role: "user", Content: "retain A; repair only B"}, orphanACGroup("batch", "repair-a", "repair-b"), orphanACResult("repair-a", "known successful A"))
	before := orphanACArchiveBytes(t, h)
	fault := &crashRecoveryWriteFault{UnifiedStore: h.store, failure: errors.New("fixture: recovery append storage unavailable")}
	cleaned := RecoverOrphanedToolCalls(fault, h.key, nil)
	assert.Greater(t, fault.attempts, 0, "instrument: the actual recovery attempted a durable append and received the fault")
	h.agent.Sessions = fault
	ts := h.turn("")
	assembled := h.al.assembleMessages(context.Background(), ts, cleaned, "", nil, nil)
	recorder, provider := cwR1OpenAI(t, 0)
	flow := cwR1Flow(h, ts, assembled, provider)
	flow.rq.prepareCallMessages()
	_, err := flow.rq.ri.rf.rt.callProviderOnce(flow.rq.ri.rf.callMessages, nil)
	assert.Error(t, err, "an uncommitted repair cannot be sanitized into a valid provider request")
	require.Len(t, recorder.requests(t), 0, "provider sees zero requests after failed repair/proof persistence")
	assert.Equal(t, before, orphanACArchiveBytes(t, h), "failed correction neither rewrites known A nor invents B")
}

func TestCrashRecovery_ConcurrentChangeDuringHashNeedsReadBarrier(t *testing.T) {
	// DESIGN Decision 2: an outside replacement after hashing but before the
	// second identity/stat check is Unknown, not proof that the effect exists.
	for _, name := range []string{"append_file", "write_file", "edit_file"} {
		t.Run(name, func(t *testing.T) {
			f := crashFileCut(t, name)
			p := &crashScript{}
			p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
				if i == 0 {
					return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("unsafe-after-unstable-read", "append_file", map[string]any{"path": f.path, "content": "UNSAFE\n"})}, FinishReason: "tool_calls"}, nil
				}
				return &providers.LLMResponse{Content: "done", FinishReason: "stop"}, nil
			}
			f.h.restore(t, f.snapshot, p)
			witness, err := tools.ReadFileEffectWitness(f.h.home, f.record.SessionID, f.assistant, "uncertain")
			require.NoError(t, err, "use the genuine native tool's durable witness, never an invented descriptor")
			ctx := tools.WithWorkspaceID(tools.WithTurnWorkspaceDir(context.Background(), witness.WorkDir), witness.WorkspaceID)
			var adapter tools.FileEffectReconciler
			switch name {
			case "append_file":
				adapter = tools.NewAppendFileTool(witness.WorkDir, true)
			case "write_file":
				adapter = tools.NewWriteFileTool(witness.WorkDir, true)
			case "edit_file":
				adapter = tools.NewEditFileTool(witness.WorkDir, true)
			}
			stable := adapter.ReconcileFileEffect(ctx, witness)
			require.Equal(t, tools.FileEffectPresent, stable.Disposition, "instrument: identical target/witness/policy is positively provable before the outside change")
			bytes, err := os.ReadFile(f.path)
			require.NoError(t, err)
			require.Equal(t, f.after, string(bytes), "stable readback itself performs no target write")

			const outside = "OUTSIDE_WRITER_REPLACED_THE_TARGET\n"
			var replacements atomic.Int32
			testhooks.ObserveFileEffectHashRead(t, func(target string) {
				if target != witness.Target || !replacements.CompareAndSwap(0, 1) {
					return
				}
				// Real outside-writer I/O, not a managed tool or an adapter result.
				// Atomic replacement guarantees an inode change without a clock race.
				require.NoError(t, fileutil.WriteFileAtomic(target, []byte(outside), 0600))
			})
			f.h.recover(t)
			record := f.h.record(t, f.record.SessionID)
			if owner := f.h.al.executionDispositionFor(f.h.al.executionClaimFor(record)); owner != nil {
				crashAwaitDone(t, owner.done)
			}
			record = f.h.record(t, f.record.SessionID)
			assert.Equal(t, int32(1), replacements.Load(), "required fileEffectAfterHashReadTestHook did not observe native recovery's post-hash/pre-stat boundary")
			assert.Equal(t, session.LifecycleState("interrupted"), record.State, "unstable proof must hold before helper dispatch")
			assert.Equal(t, "reconciliation_required", crashFields(t, record)["interruption_reason"])
			assert.Equal(t, f.record.Generation, record.Generation, "outside changes do not authorize G+1")
			assert.Equal(t, f.record.ExecutionID, record.ExecutionID, "no new execution can precede reconciliation")
			assert.Empty(t, p.calls(), "no provider request or automatic write after unstable readback")
			bytes, err = os.ReadFile(f.path)
			require.NoError(t, err)
			assert.Equal(t, outside, string(bytes), "recovery preserves the outside writer's bytes and never appends UNSAFE")

			view, err := f.h.al.GetSessionStore().WindowView(context.Background(), record.SessionID)
			require.NoError(t, err)
			var observations []map[string]any
			for _, slot := range view.Live {
				if slot.Message.Role == "tool" && slot.Message.ToolCallID == "uncertain" {
					var observation map[string]any
					require.NoError(t, json.Unmarshal([]byte(slot.Message.Content), &observation))
					observations = append(observations, observation)
				}
			}
			require.Len(t, observations, 1, "one engine observation for the exact missing result occurrence")
			assert.Equal(t, "tool_recovery_observation", observations[0]["type"])
			assert.Equal(t, tools.FileEffectUnknown, observations[0]["disposition"], "concurrent change is never effect_present or effect_absent")
			assert.Equal(t, false, observations[0]["original_result_recovered"], "current readback is not the lost historical return")
			assert.Equal(t, crashFields(t, f.assistant), observations[0]["assistant"])
		})
	}
}
