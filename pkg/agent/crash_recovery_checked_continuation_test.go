//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// The JSON observation shape is the backend's declared native model boundary
// (session_recovery_observation.go::recoveryObservation). The decision oracles
// are DESIGN Decision 2: exact current postimage -> present; exact DISTINCT
// preimage -> absent; divergent bytes -> Unknown. Never recover a historical
// return, and never let model prose replace the code-owned readback.
type crashMutationCounter struct {
	mu     sync.Mutex
	writes int
}

func (c *crashMutationCounter) BeforeTool(_ context.Context, r *ToolCallHookRequest) (*ToolCallHookRequest, HookDecision, error) {
	if r.Tool == "append_file" || r.Tool == "write_file" || r.Tool == "edit_file" {
		c.mu.Lock()
		c.writes++
		c.mu.Unlock()
	}
	return r, HookDecision{Action: HookActionContinue}, nil
}
func (c *crashMutationCounter) AfterTool(_ context.Context, r *ToolResultHookResponse) (*ToolResultHookResponse, HookDecision, error) {
	return r, HookDecision{Action: HookActionContinue}, nil
}
func (c *crashMutationCounter) count() int { c.mu.Lock(); defer c.mu.Unlock(); return c.writes }

func TestCrashRecovery_CheckedEffectsControlAutomaticContinuation(t *testing.T) {
	for _, name := range []string{"append_file", "write_file", "edit_file"} {
		t.Run(name, func(t *testing.T) {
			for _, state := range []string{"effect_present", "effect_absent", "unknown"} {
				t.Run(state, func(t *testing.T) {
					f := crashFileCut(t, name)
					f.witnessFile(t, f.snapshot) // Missing implementation is loud, never a compile error or skip.
					observed := make(chan map[string]any, 1)
					p := &crashScript{entered: make(chan int, 8)}
					p.next = func(i int, messages []providers.Message) (*providers.LLMResponse, error) {
						if i != 0 {
							return &providers.LLMResponse{Content: "continued only after checked recovery", FinishReason: "stop"}, nil
						}
						var facts map[string]any
						for _, m := range messages {
							if m.Role == "tool" && m.ToolCallID == "uncertain" {
								var value map[string]any
								if err := json.Unmarshal([]byte(m.Content), &value); err != nil {
									return nil, fmt.Errorf("recovered missing result is not a typed observation: %w", err)
								}
								if value["type"] == "tool_recovery_observation" {
									facts = value
								}
							}
						}
						observed <- facts
						if facts == nil {
							return nil, fmt.Errorf("missing occurrence lacks a code-authored recovery observation")
						}
						if facts["disposition"] == "effect_absent" {
							return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("new-authorized-write", name, f.args)}, FinishReason: "tool_calls"}, nil
						}
						return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("read-after-proof", "read_file", map[string]any{"path": f.path})}, FinishReason: "tool_calls"}, nil
					}
					f.h.restore(t, f.snapshot, p)
					current := f.after
					if state == "effect_absent" {
						current = f.before
					}
					if state == "unknown" {
						current = f.after + "external-change\n"
					}
					require.NoError(t, os.WriteFile(f.path, []byte(current), 0600))
					counter := &crashMutationCounter{}
					require.NoError(t, f.h.al.MountHook(NamedHook("observe-recovered-mutations", counter)))
					f.h.recover(t)
					got := f.h.record(t, f.record.SessionID)
					if owner := f.h.al.executionDispositionFor(f.h.al.executionClaimFor(got)); owner != nil {
						crashAwaitDone(t, owner.done)
					}
					if state == "unknown" {
						assert.Equal(t, session.LifecycleState("interrupted"), got.State)
						assert.Equal(t, "reconciliation_required", crashFields(t, got)["interruption_reason"])
						assert.Equal(t, 0, len(p.calls()), "Unknown stops before model assembly/dispatch, rather than asking a model to reconcile")
						assert.Equal(t, 0, counter.count(), "no automatic writes across divergent current state")
						bytes, err := os.ReadFile(f.path)
						require.NoError(t, err)
						assert.Equal(t, current, string(bytes))
						return
					}
					require.Equal(t, 2, len(p.calls()), "a proved current effect allows normal native continuation")
					facts := crashAwait(t, observed)
					require.NotNil(t, facts)
					assert.Equal(t, state, facts["disposition"], "automatic continuation uses the machine's exact current-state proof")
					assert.Equal(t, false, facts["original_result_recovered"], "even present bytes do not recover the lost historical return")
					assert.Equal(t, crashImage(current, true), facts["current_image"], "provider receives the whole-file readback facts")
					assert.Equal(t, crashFields(t, f.assistant), facts["assistant"], "observation binds to the original assistant occurrence, not just a reused call id")
					wantWrites := 0
					if state == "effect_absent" {
						wantWrites = 1
					}
					assert.Equal(t, wantWrites, counter.count(), "present: do not redo; absent: exactly one new authorized mutation")
					bytes, err := os.ReadFile(f.path)
					require.NoError(t, err)
					assert.Equal(t, f.after, string(bytes), "the intended complete image exists exactly once after recovery")
				})
			}
		})
	}
}
