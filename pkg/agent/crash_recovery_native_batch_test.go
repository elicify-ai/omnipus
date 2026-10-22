//go:build goolm && stdjson

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func TestCrashRecovery_NativeBatchPreservesAAndReconcilesB(t *testing.T) {
	for _, cut := range []string{"before_B_replacement", "B_applied_before_result"} {
		t.Run(cut, func(t *testing.T) {
			gate := make(chan struct{})
			p := &crashScript{gate: gate, entered: make(chan int, 8)}
			h := crashNew(t, p)
			t.Cleanup(func() {
				select {
				case <-gate:
				default:
					close(gate)
				}
			})
			work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
			require.NoError(t, err)
			path := filepath.Join(work, "batch.txt")
			require.NoError(t, os.WriteFile(path, nil, 0600))
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			open := sync.OnceFunc(func() { close(release) })
			t.Cleanup(open)
			if cut == "before_B_replacement" {
				tool := tools.NewAppendFileTool(work, true)
				setter := reflect.ValueOf(tool).MethodByName("SetFileEffectBeforeWriteHook")
				if !setter.IsValid() {
					t.Fatal("BLOCKED: native B post-witness/pre-replacement observer not implemented — required by crash-recovery DESIGN RED D/Q15")
				}
				kind := setter.Type().In(0)
				hook := reflect.MakeFunc(kind, func(v []reflect.Value) []reflect.Value {
					var w map[string]any
					raw, e := json.Marshal(v[0].Interface())
					if e != nil {
						return []reflect.Value{reflect.ValueOf(e)}
					}
					if e = json.Unmarshal(raw, &w); e != nil {
						return []reflect.Value{reflect.ValueOf(e)}
					}
					if w["tool_call_id"] == "batch-b" {
						entered <- struct{}{}
						<-release
					}
					return []reflect.Value{reflect.Zero(kind.Out(0))}
				})
				restore := setter.Call([]reflect.Value{hook})[0]
				t.Cleanup(func() { restore.Call(nil) })
				h.al.GetRegistry().primaryChatAgentForTest().Tools.RegisterReplacing(tool)
			} else {
				barrier := &crashFileResultBarrier{tool: "append_file", matchContent: "B\n", entered: make(chan *ToolResultHookResponse, 1), release: release, signal: entered}
				require.NoError(t, h.al.MountHook(NamedHook("native-batch-result-cut", barrier)))
			}
			p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
				if i != 0 {
					return &providers.LLMResponse{Content: "old process retired after cut", FinishReason: "stop"}, nil
				}
				return &providers.LLMResponse{ToolCalls: []providers.ToolCall{
					crashCall("batch-a", "append_file", map[string]any{"path": path, "content": "A\n"}), crashCall("batch-b", "append_file", map[string]any{"path": path, "content": "B\n"})}, FinishReason: "tool_calls"}, nil
			}
			parent := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
			launched, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent, TargetAgentID: testDefaultAgentID, Task: "append A then B exactly once", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "native-batch"}})
			require.NoError(t, err)
			crashAppend(t, h, launched.SessionID, providers.Message{Role: "user", Content: "independent old turn"})
			crashAppend(t, h, launched.SessionID, providers.Message{Role: "assistant", Content: "old settled answer"})
			crashAppend(t, h, launched.SessionID, providers.Message{Role: "user", Content: "append A then B exactly once"})
			d, err := NewSteerLauncher(h.al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
			require.NoError(t, err)
			require.Equal(t, steer.DispatchRunning, d.State)
			crashAwait(t, p.entered)
			rec := h.record(t, launched.SessionID)
			owner := h.al.executionDispositionFor(h.al.executionClaimFor(rec))
			require.NotNil(t, owner)
			close(gate)
			crashAwait(t, entered)
			view, err := h.al.GetSessionStore().WindowView(context.Background(), rec.SessionID)
			require.NoError(t, err)
			var a []session.ModelSlot
			for _, slot := range view.Live {
				if slot.Message.Role == "tool" && slot.Message.ToolCallID == "batch-a" {
					a = append(a, slot)
				}
			}
			require.Len(t, a, 1, "A's real result is committed before B's crash cut")
			transcript := filepath.Join(h.al.GetSessionStore().BaseDir(), rec.SessionID, "transcript.jsonl")
			original, err := os.ReadFile(transcript)
			require.NoError(t, err)
			snapshot := h.snapshot(t)
			open()
			crashAwaitDone(t, owner.done)
			next := &crashScript{}
			next.next = func(i int, m []providers.Message) (*providers.LLMResponse, error) {
				if i != 0 {
					return &providers.LLMResponse{Content: "batch finished", FinishReason: "stop"}, nil
				}
				hasA := false
				var disposition string
				for _, v := range m {
					if v.Role == "tool" && v.ToolCallID == "batch-a" && v.Content == a[0].Message.Content {
						hasA = true
					}
					if v.Role == "tool" && v.ToolCallID == "batch-b" {
						var ob map[string]any
						if e := json.Unmarshal([]byte(v.Content), &ob); e != nil {
							return nil, e
						}
						disposition, _ = ob["disposition"].(string)
					}
				}
				name := "read_file"
				args := map[string]any{"path": path}
				if !hasA {
					name = "append_file"
					args["content"] = "A\n"
				} else if disposition == "effect_absent" {
					name = "append_file"
					args["content"] = "B\n"
				}
				return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("new-batch-decision", name, args)}, FinishReason: "tool_calls"}, nil
			}
			h.restore(t, snapshot, next)
			h.recover(t)
			current := h.record(t, rec.SessionID)
			if own := h.al.executionDispositionFor(h.al.executionClaimFor(current)); own != nil {
				crashAwaitDone(t, own.done)
			}
			require.Len(t, next.calls(), 2, "real resumed native model request plus one settled new step")
			input := next.calls()[0]
			require.NoError(t, validateWindowGroups(input))
			var seenA []providers.Message
			var seenB []map[string]any
			for _, m := range input {
				if m.Role == "tool" && m.ToolCallID == "batch-a" {
					seenA = append(seenA, m)
				}
				if m.Role == "tool" && m.ToolCallID == "batch-b" {
					var ob map[string]any
					require.NoError(t, json.Unmarshal([]byte(m.Content), &ob))
					seenB = append(seenB, ob)
				}
			}
			assert.Equal(t, []providers.Message{a[0].Message}, seenA, "actual first resumed native input retains A's exact original success")
			require.Len(t, seenB, 1, "only B's missing occurrence receives an engine observation")
			want := "effect_present"
			if cut == "before_B_replacement" {
				want = "effect_absent"
			}
			assert.Equal(t, want, seenB[0]["disposition"])
			assert.Equal(t, false, seenB[0]["original_result_recovered"])
			final, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, "A\nB\n", string(final), "A and B each land once, never historical call replay")
			after, err := os.ReadFile(transcript)
			require.NoError(t, err)
			assert.True(t, bytes.HasPrefix(after, original), "recovery appends corrections and never rewrites original archive bytes")
			inst := h.al.GetRegistry().primaryChatAgentForTest()
			trim, changed := h.al.trimWindowChecked(context.Background(), inst, rec.SessionID, rec.SessionID, true)
			require.NoError(t, trim.Err)
			require.True(t, changed, "old independent whole turn gives a real legal trim")
			h.reboot(t, &crashScript{})
			fresh, err := h.al.GetSessionStore().WindowView(context.Background(), rec.SessionID)
			require.NoError(t, err)
			var retained []session.ModelSlot
			for _, slot := range fresh.Live {
				if slot.Message.Role == "tool" && slot.Message.ToolCallID == "batch-a" {
					retained = append(retained, slot)
				}
			}
			require.Len(t, retained, 1)
			assert.Equal(t, a[0].Addr, retained[0].Addr)
			assert.Equal(t, a[0].Ordinal, retained[0].Ordinal, "trim/reload preserves A's original archive identity")
		})
	}
}
